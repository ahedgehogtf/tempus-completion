package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstats"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/cmd/tempus-completion-fetcher/rqlitecompletionstore"
	"tempus-completion/cmd/tempus-statsd/httpserveutil"
	"tempus-completion/cmd/tempus-statsd/statsdhttp"
	"tempus-completion/cmd/tempus-statsd/templateutil"
	"tempus-completion/steamidutil"
	"tempus-completion/tempushttp"
	"tempus-completion/tempushttprpc"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := NewFlagSet("statsd")

	var rqliteaddr string
	var certpath string
	var keypath string
	var port string
	var address string

	flags.StringVar(&rqliteaddr, "rqlite-address", "", "")
	flags.StringVar(&certpath, "cert", "", "")
	flags.StringVar(&keypath, "key", "", "")
	flags.StringVar(&port, "port", "9876", "")
	flags.StringVar(&address, "address", "0.0.0.0", "")

	ok, err := ParseArgs(flags, args, stderr, "")
	if err != nil {
		return fmt.Errorf("parse args: %w", err)
	}

	if !ok {
		return nil
	}

	if rqliteaddr == "" {
		return fmt.Errorf("-rqlite-address must be set")
	}

	mux := http.NewServeMux()

	pt, err := parseTemplates()
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}

	store, err := rqlitecompletionstore.New(rqliteaddr)
	if err != nil {
		return fmt.Errorf("new completion store: %w", err)
	}

	httpc := http.Client{}

	client := tempushttprpc.NewClient(httpc, "")

	h := &Handler{
		templates: pt,
		store:     store,
		client:    client,
	}

	httpserveutil.Register(mux, stdout, h)

	addr := fmt.Sprintf("[%s]:%s", address, port)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       0,
		ReadHeaderTimeout: 0,
		WriteTimeout:      0,
		IdleTimeout:       0,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	if certpath != "" && keypath != "" {
		fmt.Fprintf(stdout, "Listening on tcp with tls at %s\n", listener.Addr().String())

		if err := server.ServeTLS(listener, certpath, keypath); err != nil {
			return fmt.Errorf("listen and serve tls: %w", err)
		}
	} else {
		fmt.Fprintf(stdout, "Listening on tcp at %s\n", listener.Addr().String())

		if err := server.Serve(listener); err != nil {
			return fmt.Errorf("listen and serve: %w", err)
		}
	}

	return nil
}

type Store interface {
	GetPlayerResults(ctx context.Context, playerID uint64, zoneTypes []string, tiers, classes []uint8) ([]completionstore.PlayerClassZoneResult, bool, error)
	GetPlayerMapClassResults(ctx context.Context, playerID, mapID uint64, class tempushttp.ClassType) ([]completionstore.PlayerClassZoneResult, bool, error)
	GetPlayerRecentResults(ctx context.Context, playerID uint64) ([]completionstore.PlayerClassZoneResult, bool, error)
	GetPlayerBySteamID(ctx context.Context, steamID string) (uint64, bool, error)
	GetPlayerClassZoneResults(ctx context.Context, playerID uint64, zoneTypes []string, tiers, classes []uint8) ([]completionstore.PlayerClassZoneResult, bool, error)
}

type Handler struct {
	client    *tempushttprpc.Client
	templates PageTemplates
	store     Store
}

var (
	//go:embed static/*
	staticFS embed.FS
)

func (h *Handler) serveIndexPage(w http.ResponseWriter, r *http.Request) error {
	type pageData struct {
		RecentPlayers recentPlayersCookie
	}

	var recentPlayers recentPlayersCookie

	if _, err := unmarshalRecentPlayersCookie(r, &recentPlayers); err != nil {
		c := &http.Cookie{
			Name:     recentPlayersCookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Time{},
			HttpOnly: true,
		}

		http.SetCookie(w, c)
	}

	slices.Reverse(recentPlayers.Players)

	data := pageData{
		RecentPlayers: recentPlayers,
	}

	if err := h.templates.index.Execute(w, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

func (h *Handler) serveSearchPage(w http.ResponseWriter, r *http.Request) error {
	if err := h.templates.search.Execute(w, nil); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

func (h *Handler) serveSearchResultsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	query := q.Get("name")
	if query == "" {
		return httpserveutil.BadRequest(w, "must specify name")
	}

	ctx := r.Context()

	if strings.HasPrefix(query, "STEAM") {
		playerID, ok, err := h.store.GetPlayerBySteamID(ctx, query)
		if err != nil {
			return httpserveutil.InternalError(w, "get player by Steam ID: %w", err)
		}

		if !ok {
			return httpserveutil.BadRequest(w, "could not find Player ID associated with that Steam ID")
		}

		addr := fmt.Sprintf("/player?playerid=%d", playerID)

		http.Redirect(w, r, addr, http.StatusPermanentRedirect)
		return nil
	}

	result, err := h.client.SearchPlayersAndMaps(ctx, query)
	if err != nil {
		return httpserveutil.BadRequest(w, "search players and maps: %w", err)
	}

	type pageData struct {
		Result *tempushttp.PlayersAndMapsSearchResponse
	}

	data := pageData{
		Result: result,
	}

	if err := h.templates.results.Execute(w, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

type recentPlayersCookie struct {
	Players []recentPlayersCookiePlayer `json:"players"`
}

type recentPlayersCookiePlayer struct {
	Name     string `json:"name"`
	PlayerID uint64 `json:"player_id"`
}

const recentPlayersCookieName = "recent-players"

func unmarshalRecentPlayersCookie(r *http.Request, recentPlayers *recentPlayersCookie) (*http.Cookie, error) {
	cookie, err := r.Cookie(recentPlayersCookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			c := &http.Cookie{
				Name:     recentPlayersCookieName,
				Value:    "",
				Path:     "/",
				Secure:   false,
				HttpOnly: false,
			}

			return c, nil
		}

		return nil, fmt.Errorf("parse cookie: %w", err)
	}

	b, err := base64.URLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	if err := json.Unmarshal(b, recentPlayers); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	return cookie, nil
}

func (h *Handler) servePlayerPage(w http.ResponseWriter, r *http.Request) error {
	var recentPlayers recentPlayersCookie

	cookie, err := unmarshalRecentPlayersCookie(r, &recentPlayers)
	if err != nil {
		cookie = &http.Cookie{
			Name:     recentPlayersCookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Time{},
			HttpOnly: true,
		}

		http.SetCookie(w, cookie)
	}

	q := r.URL.Query()

	pid := q.Get("playerid")
	if pid == "" {
		return httpserveutil.BadRequest(w, "must specify playerID")
	}

	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID: %w", err)
	}

	ctx := r.Context()

	stats, err := h.client.GetPlayerStats(ctx, playerID)
	if err != nil {
		return httpserveutil.BadRequest(w, "search players and maps: %w", err)
	}

	{
		rp := recentPlayersCookiePlayer{
			Name:     stats.PlayerInfo.Name,
			PlayerID: playerID,
		}

		players := make([]recentPlayersCookiePlayer, 0, 5)

		for _, p := range recentPlayers.Players {
			if p.PlayerID == playerID {
				continue
			}

			players = append(players, p)
		}
		players = append(players, rp)
		if len(players) > 4 {
			players = players[1:]
		}

		recentPlayers.Players = players
	}

	steamID64, err := steamidutil.IDToInt64(stats.PlayerInfo.SteamID)
	if err != nil {
		return httpserveutil.BadRequest(w, "convert steam id to uint64: %w", err)
	}

	results, _, err := h.store.GetPlayerRecentResults(ctx, playerID)
	if err != nil {
		return httpserveutil.InternalError(w, "get player recent results: %w", err)
	}

	type pageData struct {
		PlayerID          uint64
		PlayerName        string
		SteamID64         int64
		Country           string
		Joined            time.Time
		LastActive        time.Time
		OverallRank       uint32
		SoldierRank       uint32
		SoldierTitle      string
		SoldierTitleColor string
		DemomanRank       uint32
		DemomanTitle      string
		DemomanTitleColor string

		RecentResults []completionstore.PlayerClassZoneResult
	}

	data := pageData{
		PlayerID:          playerID,
		PlayerName:        stats.PlayerInfo.Name,
		SteamID64:         steamID64,
		Country:           stats.PlayerInfo.Country,
		Joined:            time.Unix(int64(stats.PlayerInfo.FirstSeen), 0),
		LastActive:        time.Unix(int64(stats.PlayerInfo.LastSeen), 0),
		OverallRank:       stats.OverallRankInfo.Rank,
		SoldierRank:       stats.ClassRankInfo.Soldier.Rank,
		SoldierTitle:      stats.ClassRankInfo.Soldier.Title,
		SoldierTitleColor: "var(--text-color)",
		DemomanRank:       stats.ClassRankInfo.Demoman.Rank,
		DemomanTitle:      stats.ClassRankInfo.Demoman.Title,
		DemomanTitleColor: "var(--text-color)",
		RecentResults:     results,
	}

	b, err := json.Marshal(recentPlayers)
	if err != nil {
		return httpserveutil.InternalError(w, "marshal recent players: %w", err)
	}

	cookie.Value = base64.URLEncoding.EncodeToString(b)
	http.SetCookie(w, cookie)

	if err := h.templates.player.Execute(w, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

var (
	zoneTypePriorities = map[tempushttp.ZoneType]int{
		tempushttp.ZoneTypeMap:    1,
		tempushttp.ZoneTypeCourse: 2,
		tempushttp.ZoneTypeBonus:  3,
		tempushttp.ZoneTypeTrick:  4,
	}
)

func (h *Handler) serveMapPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	pid := q.Get("playerid")
	if pid == "" {
		return httpserveutil.BadRequest(w, "must specify playerID")
	}
	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID: %w", err)
	}

	mid := q.Get("mapid")
	mapID, err := strconv.ParseUint(mid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed mapID: %w", err)
	}

	class := q.Get("class")

	var ct tempushttp.ClassType

	switch class {
	case "soldier":
		ct = tempushttp.ClassTypeSoldier
	case "demoman":
		ct = tempushttp.ClassTypeDemoman
	default:
		return httpserveutil.BadRequest(w, "class '%s' is not supported", class)
	}

	ctx := r.Context()

	results, ok, err := h.store.GetPlayerMapClassResults(ctx, playerID, mapID, ct)

	if err != nil {
		return httpserveutil.InternalError(w, "get results: %w", err)
	}

	if !ok {
		if err := h.templates.index.Execute(w, nil); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}

		return nil
	}

	type pageData struct {
		Results  []completionstore.PlayerClassZoneResult
		MapName  string
		PlayerID uint64
		Class    string
	}

	d := pageData{
		PlayerID: playerID,
		Class:    class,
		MapName:  results[0].MapName,
		Results:  results,
	}

	sort.Slice(d.Results, func(i, j int) bool {
		pi := zoneTypePriorities[d.Results[i].ZoneType]
		pj := zoneTypePriorities[d.Results[j].ZoneType]

		if pi != pj {
			return pi < pj
		}

		return d.Results[i].Tier < d.Results[j].Tier
	})

	if err := h.templates.classmap.Execute(w, d); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

func (h *Handler) serveResultsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	pid := q.Get("playerid")
	if pid == "" {
		return httpserveutil.BadRequest(w, "must specify playerID")
	}
	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID: %w", err)
	}

	zoneTypes := q["zone-type"]

	if len(zoneTypes) == 0 {
		zoneTypes = []string{"map", "course", "bonus"}
	}

	type pageFilters struct {
		Tier1Checked        bool
		Tier2Checked        bool
		Tier3Checked        bool
		Tier4Checked        bool
		Tier5Checked        bool
		Tier6Checked        bool
		MapZoneChecked      bool
		CourseZoneChecked   bool
		BonusZoneChecked    bool
		TrickZoneChecked    bool
		SoldierChecked      bool
		DemomanChecked      bool
		TopTimesOnlyChecked bool
		Sort                string
		Measurement         string
	}

	var pf pageFilters

	for _, v := range zoneTypes {
		switch v {
		case "map":
			pf.MapZoneChecked = true
		case "course":
			pf.CourseZoneChecked = true
		case "bonus":
			pf.BonusZoneChecked = true
		case "trick":
			pf.TrickZoneChecked = true
		default:
			return httpserveutil.BadRequest(w, "zone-type '%s' is not supported", v)
		}
	}

	tiers := q["tier"]
	queryTiers := make([]uint8, 0, len(tiers))

	if len(tiers) == 0 {
		tiers = []string{"t1", "t2", "t3", "t4", "t5", "t6"}
	}

	for _, v := range tiers {
		switch v {
		case "t1":
			queryTiers = append(queryTiers, 1)
			pf.Tier1Checked = true
		case "t2":
			queryTiers = append(queryTiers, 2)
			pf.Tier2Checked = true
		case "t3":
			queryTiers = append(queryTiers, 3)
			pf.Tier3Checked = true
		case "t4":
			queryTiers = append(queryTiers, 4)
			pf.Tier4Checked = true
		case "t5":
			queryTiers = append(queryTiers, 5)
			pf.Tier5Checked = true
		case "t6":
			queryTiers = append(queryTiers, 6)
			pf.Tier6Checked = true
		default:
			return httpserveutil.BadRequest(w, "tier '%s' is not supported", v)
		}
	}

	classes := q["class"]
	var queryClasses []uint8

	switch len(classes) {
	case 2, 0:
		pf.SoldierChecked = true
		pf.DemomanChecked = true

		queryClasses = []uint8{3, 4}

	case 1:
		switch classes[0] {
		case "soldier":
			pf.SoldierChecked = true

			queryClasses = []uint8{3}
		case "demoman":
			pf.DemomanChecked = true

			queryClasses = []uint8{4}
		default:
			return httpserveutil.BadRequest(w, "class '%s' is not supported", classes[0])
		}
	default:
		return httpserveutil.BadRequest(w, "must specify 1 or 2 classes")
	}

	ctx := r.Context()

	results, ok, err := h.store.GetPlayerResults(ctx, playerID, zoneTypes, queryTiers, queryClasses)
	if err != nil {
		return httpserveutil.InternalError(w, "get completions: %w", err)
	}

	if !ok {
		if err := h.templates.index.Execute(w, nil); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}

		return nil
	}

	pf.TopTimesOnlyChecked = q.Get("top-times-only") == "true"

	if pf.TopTimesOnlyChecked {
		var j int

		for i := 0; i < len(results); i++ {
			r := results[i]
			if r.Rank > 10 {
				continue
			}

			results[j] = r
			j++
		}

		results = results[:j]
	}

	pf.Sort = q.Get("sort")
	if sf, ok := resultsSortFuncs[pf.Sort]; ok {
		sf(results)
	}

	format := q.Get("format")

	switch format {
	case "json":
		resultshttp := make([]statsdhttp.PlayerClassZoneResult, 0, len(results))

		for _, r := range results {
			resultshttp = append(resultshttp, playerClassZoneResultToHTTP(r))
		}

		response := statsdhttp.ResultsResponse{
			Results: resultshttp,
		}

		enc := json.NewEncoder(w)

		if err := enc.Encode(response); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	default:
		type pageData struct {
			Results  []completionstore.PlayerClassZoneResult
			PlayerID uint64
			Filters  pageFilters
		}

		d := pageData{
			PlayerID: playerID,
			Results:  results,
			Filters:  pf,
		}

		if err := h.templates.playerResults.Execute(w, d); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}
	}

	return nil
}

func SortSoldierTierAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.Tiers < stats[j].Soldier.Tiers
	})
}

func SortDemomanTierAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.Tiers < stats[j].Demoman.Tiers
	})
}

func SortBothTierAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.Tiers < stats[j].Demoman.Tiers
		s := stats[i].Soldier.Tiers < stats[j].Soldier.Tiers
		return d && s
	})
}

func SortSoldierTierDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.Tiers > stats[j].Soldier.Tiers
	})
}

func SortDemomanTierDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.Tiers > stats[j].Demoman.Tiers
	})
}

func SortBothTierDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.Tiers > stats[j].Demoman.Tiers
		s := stats[i].Soldier.Tiers > stats[j].Soldier.Tiers
		return d && s
	})
}

func SortMapNameAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].MapName < stats[j].MapName
	})
}

func SortMapNameDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].MapName > stats[j].MapName
	})
}

func SortSoldierCompletionDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.ZonesFinishedPercentage > stats[j].Soldier.ZonesFinishedPercentage
	})
}

func SortSoldierCompletionAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.ZonesFinishedPercentage < stats[j].Soldier.ZonesFinishedPercentage
	})
}
func SortDemomanCompletionDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.ZonesFinishedPercentage > stats[j].Demoman.ZonesFinishedPercentage
	})
}

func SortDemomanCompletionAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.ZonesFinishedPercentage < stats[j].Demoman.ZonesFinishedPercentage
	})
}

func SortBothCompletionDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.ZonesFinishedPercentage
		d1 := stats[j].Demoman.ZonesFinishedPercentage
		s0 := stats[i].Soldier.ZonesFinishedPercentage
		s1 := stats[j].Soldier.ZonesFinishedPercentage

		return max8(d0, s0) > max8(d1, s1)
	})
}

func SortBothCompletionAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.ZonesFinishedPercentage
		d1 := stats[j].Demoman.ZonesFinishedPercentage
		s0 := stats[i].Soldier.ZonesFinishedPercentage
		s1 := stats[j].Soldier.ZonesFinishedPercentage

		return max8(d0, s0) < max8(d1, s1)
	})
}

func SortSoldierCompletionCountDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.MostPopularCompletions > stats[j].Soldier.MostPopularCompletions
	})
}

func SortSoldierCompletionCountAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.LeastPopularCompletions < stats[j].Soldier.LeastPopularCompletions
	})
}
func SortDemomanCompletionCountDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.MostPopularCompletions > stats[j].Demoman.MostPopularCompletions
	})
}

func SortDemomanCompletionCountAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.LeastPopularCompletions < stats[j].Demoman.LeastPopularCompletions
	})
}

func SortBothCompletionCountDescending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.MostPopularCompletions
		d1 := stats[j].Demoman.MostPopularCompletions
		s0 := stats[i].Soldier.MostPopularCompletions
		s1 := stats[j].Soldier.MostPopularCompletions

		return max32(d0, s0) > max32(d1, s1)
	})
}

func SortBothCompletionCountAscending(stats []completionstats.PlayerMapResultStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.LeastPopularCompletions
		d1 := stats[j].Demoman.LeastPopularCompletions
		s0 := stats[i].Soldier.LeastPopularCompletions
		s1 := stats[j].Soldier.LeastPopularCompletions

		return max32(d0, s0) < max32(d1, s1)
	})
}

func max8(a, b uint8) uint8 {
	if a > b {
		return a
	}

	return b
}

func max32(a, b uint32) uint32 {
	if a > b {
		return a
	}

	return b
}

type sortFuncsKey struct {
	sortType string
	class    string
}

var (
	completionSortFuncs = map[sortFuncsKey]SortFunc{
		{sortType: "tier-ascending", class: "both"}:                 SortBothTierAscending,
		{sortType: "tier-ascending", class: "soldier"}:              SortSoldierTierAscending,
		{sortType: "tier-ascending", class: "demoman"}:              SortDemomanTierAscending,
		{sortType: "tier-descending", class: "both"}:                SortBothTierDescending,
		{sortType: "tier-descending", class: "soldier"}:             SortSoldierTierDescending,
		{sortType: "tier-descending", class: "demoman"}:             SortDemomanTierDescending,
		{sortType: "map-name-ascending", class: "both"}:             SortMapNameAscending,
		{sortType: "map-name-ascending", class: "soldier"}:          SortMapNameAscending,
		{sortType: "map-name-ascending", class: "demoman"}:          SortMapNameAscending,
		{sortType: "map-name-descending", class: "both"}:            SortMapNameDescending,
		{sortType: "map-name-descending", class: "soldier"}:         SortMapNameDescending,
		{sortType: "map-name-descending", class: "demoman"}:         SortMapNameDescending,
		{sortType: "", class: "both"}:                               SortMapNameAscending,
		{sortType: "", class: "soldier"}:                            SortMapNameAscending,
		{sortType: "", class: "demoman"}:                            SortMapNameAscending,
		{sortType: "completion-ascending", class: "both"}:           SortBothCompletionAscending,
		{sortType: "completion-ascending", class: "soldier"}:        SortSoldierCompletionAscending,
		{sortType: "completion-ascending", class: "demoman"}:        SortDemomanCompletionAscending,
		{sortType: "completion-descending", class: "both"}:          SortBothCompletionDescending,
		{sortType: "completion-descending", class: "soldier"}:       SortSoldierCompletionDescending,
		{sortType: "completion-descending", class: "demoman"}:       SortDemomanCompletionDescending,
		{sortType: "completion-count-ascending", class: "both"}:     SortBothCompletionCountAscending,
		{sortType: "completion-count-ascending", class: "soldier"}:  SortSoldierCompletionCountAscending,
		{sortType: "completion-count-ascending", class: "demoman"}:  SortDemomanCompletionCountAscending,
		{sortType: "completion-count-descending", class: "both"}:    SortBothCompletionCountDescending,
		{sortType: "completion-count-descending", class: "soldier"}: SortSoldierCompletionCountDescending,
		{sortType: "completion-count-descending", class: "demoman"}: SortDemomanCompletionCountDescending,
	}

	resultsSortFuncs = map[string]ResultsSortFunc{
		"map-name-ascending":          SortMapNameAscendingResults,
		"map-name-descending":         SortMapNameDescendingResults,
		"completion-count-ascending":  SortCompletionCountAscendingResults,
		"completion-count-descending": SortCompletionCountDescendingResults,
		"tier-ascending":              SortTierAscendingResults,
		"tier-descending":             SortTierDescendingResults,
		"duration-ascending":          SortDurationAscendingResults,
		"duration-descending":         SortDurationDescendingResults,
		"date-ascending":              SortDateAscendingResults,
		"date-descending":             SortDateDescendingResults,
		"rank-ascending":              SortRankAscendingResults,
		"rank-descending":             SortRankDescendingResults,
		"rank-percentile-ascending":   SortRankPercentileAscendingResults,
		"rank-percentile-descending":  SortRankPercentileDescendingResults,
	}
)

func SortMapNameAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].MapName < results[j].MapName
	})
}

func SortMapNameDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].MapName > results[j].MapName
	})
}

func SortCompletionCountAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Completions < results[j].Completions
	})
}

func SortCompletionCountDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Completions > results[j].Completions
	})
}

func SortTierAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Tier < results[j].Tier
	})
}

func SortTierDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Tier > results[j].Tier
	})
}

func SortDurationAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Duration < results[j].Duration
	})
}

func SortDurationDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Duration > results[j].Duration
	})
}

func SortRankAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Rank < results[j].Rank
	})
}

func SortRankDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Rank > results[j].Rank
	})
}

func SortRankPercentileAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		ra := float64(results[i].Rank)
		rb := float64(results[j].Rank)

		ca := float64(results[i].Completions)
		cb := float64(results[j].Completions)

		if ca == 0 {
			return false
		}

		if cb == 0 {
			return true
		}

		return (ra / ca) < (rb / cb)
	})
}

func SortRankPercentileDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		ra := float64(results[i].Rank)
		rb := float64(results[j].Rank)

		ca := float64(results[i].Completions)
		cb := float64(results[j].Completions)

		if ca == 0 {
			return true
		}

		if cb == 0 {
			return false
		}

		return (ra / ca) > (rb / cb)
	})
}

func SortDateAscendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Date.Before(results[j].Date)
	})
}

func SortDateDescendingResults(results []completionstore.PlayerClassZoneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Date.After(results[j].Date)
	})
}

type SortFunc func([]completionstats.PlayerMapResultStats)
type ResultsSortFunc func([]completionstore.PlayerClassZoneResult)

func (h *Handler) serveCompletionsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	pid := q.Get("playerid")
	if pid == "" {
		return httpserveutil.BadRequest(w, "must specify playerID")
	}

	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID")
	}

	// TODO: use bitmask for filters, too
	type pageFilters struct {
		Tier1Checked         bool
		Tier2Checked         bool
		Tier3Checked         bool
		Tier4Checked         bool
		Tier5Checked         bool
		Tier6Checked         bool
		MapZoneChecked       bool
		CourseZoneChecked    bool
		BonusZoneChecked     bool
		TrickZoneChecked     bool
		SoldierChecked       bool
		DemomanChecked       bool
		HideCompletedChecked bool
		Sort                 string
		Measurement          string
	}

	var pf pageFilters

	tiers := q["tier"]
	queryTiers := make([]uint8, 0, len(tiers))

	if len(tiers) == 0 {
		tiers = []string{"t1", "t2", "t3", "t4", "t5", "t6"}
	}

	for _, v := range tiers {
		switch v {
		case "t1":
			queryTiers = append(queryTiers, 1)
			pf.Tier1Checked = true
		case "t2":
			queryTiers = append(queryTiers, 2)
			pf.Tier2Checked = true
		case "t3":
			queryTiers = append(queryTiers, 3)
			pf.Tier3Checked = true
		case "t4":
			queryTiers = append(queryTiers, 4)
			pf.Tier4Checked = true
		case "t5":
			queryTiers = append(queryTiers, 5)
			pf.Tier5Checked = true
		case "t6":
			queryTiers = append(queryTiers, 6)
			pf.Tier6Checked = true
		default:
			return httpserveutil.BadRequest(w, "tier '%s' is not supported", v)
		}
	}

	zoneTypes := q["zone-type"]

	if len(zoneTypes) == 0 {
		zoneTypes = []string{"map", "course", "bonus"}
	}

	for _, v := range zoneTypes {
		switch v {
		case "map":
			pf.MapZoneChecked = true
		case "course":
			pf.CourseZoneChecked = true
		case "bonus":
			pf.BonusZoneChecked = true
		case "trick":
			pf.TrickZoneChecked = true
		default:
			return httpserveutil.BadRequest(w, "zone-type '%s' is not supported", v)
		}
	}

	hideCompleted := q.Get("hide-completed") == "true"
	pf.HideCompletedChecked = hideCompleted

	sortType := q.Get("sort")

	sfk := sortFuncsKey{
		sortType: sortType,
	}

	pf.Sort = sortType

	classes := q["class"]
	var queryClasses []uint8

	switch len(classes) {
	case 2, 0:
		sfk.class = "both"

		pf.SoldierChecked = true
		pf.DemomanChecked = true

		queryClasses = []uint8{3, 4}

	case 1:
		switch classes[0] {
		case "soldier":
			sfk.class = "soldier"
			pf.SoldierChecked = true

			queryClasses = []uint8{3}
		case "demoman":
			sfk.class = "demoman"
			pf.DemomanChecked = true

			queryClasses = []uint8{4}
		default:
			return httpserveutil.BadRequest(w, "class '%s' is not supported", classes[0])
		}
	default:
		return httpserveutil.BadRequest(w, "must specify 1 or 2 classes")
	}

	ctx := r.Context()

	results, _, err := h.store.GetPlayerClassZoneResults(ctx, playerID, zoneTypes, queryTiers, queryClasses)
	if err != nil {
		return httpserveutil.InternalError(w, "get completions: %w", err)
	}

	stats := completionstats.AggregateMapResultStats(results, hideCompleted)

	if sf, ok := completionSortFuncs[sfk]; ok {
		sf(stats)
	}
	measurement := q.Get("measurement")
	switch measurement {
	case "zones-finished-percentage", "points-finished-percentage":
	default:
		measurement = "zones-finished-percentage"
	}

	pf.Measurement = measurement

	format := q.Get("format")

	switch format {
	case "json":
		statshttp := make([]statsdhttp.PlayerMapResultStats, 0, len(stats))

		for _, s := range stats {
			statshttp = append(statshttp, playerMapResultStatsToHTTP(s))
		}

		response := statsdhttp.CompletionsResponse{
			Stats: statshttp,
		}

		enc := json.NewEncoder(w)

		if err := enc.Encode(response); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
	default:
		type pageData struct {
			PlayerID uint64
			Filters  pageFilters
			Stats    []completionstats.PlayerMapResultStats
		}

		d := pageData{
			PlayerID: playerID,
			Filters:  pf,
			Stats:    stats,
		}

		if err := h.templates.completions.Execute(w, d); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}
	}

	return nil
}

func playerMapResultStatsToHTTP(stats completionstats.PlayerMapResultStats) statsdhttp.PlayerMapResultStats {
	return statsdhttp.PlayerMapResultStats{
		MapID:   stats.MapID,
		MapName: stats.MapName,
		Demoman: playerClassMapResultStatsToHTTP(stats.Demoman),
		Soldier: playerClassMapResultStatsToHTTP(stats.Soldier),
	}
}

func playerClassMapResultStatsToHTTP(stats completionstats.PlayerClassMapResultStats) statsdhttp.CompletionsPlayerClassMapResultStats {
	results := make([]statsdhttp.PlayerClassZoneResult, 0, len(stats.Results))

	for _, r := range stats.Results {
		rhttp := playerClassZoneResultToHTTP(r)

		results = append(results, rhttp)
	}

	return statsdhttp.CompletionsPlayerClassMapResultStats{
		PointsTotal:              stats.PointsTotal,
		ZonesTotal:               stats.ZonesTotal,
		PointsFinished:           stats.PointsFinished,
		ZonesFinished:            stats.ZonesFinished,
		PointsFinishedPercentage: stats.PointsFinishedPercentage,
		ZonesFinishedPercentage:  stats.ZonesFinishedPercentage,
		MostPopularCompletions:   stats.MostPopularCompletions,
		LeastPopularCompletions:  stats.LeastPopularCompletions,
		CompletionsCount:         stats.CompletionsCount,
		Tiers:                    uint8(stats.Tiers),
		Results:                  results,
	}
}

func playerClassZoneResultToHTTP(result completionstore.PlayerClassZoneResult) statsdhttp.PlayerClassZoneResult {
	return statsdhttp.PlayerClassZoneResult{
		MapID:       result.MapID,
		ZoneType:    string(result.ZoneType),
		ZoneIndex:   result.ZoneIndex,
		PlayerID:    result.PlayerID,
		Class:       uint8(result.Class),
		CustomName:  result.CustomName,
		MapName:     result.MapName,
		Tier:        result.Tier,
		Rank:        result.Rank,
		Duration:    int64(result.Duration),
		Date:        result.Date.UnixMilli(),
		Completions: result.Completions,
	}
}

func (h *Handler) Routes(out io.Writer) map[string]http.Handler {
	return map[string]http.Handler{
		"/":               httpserveutil.Handle(out, h.serveIndexPage),
		"/completions":    httpserveutil.Handle(out, h.serveCompletionsPage),
		"/map":            httpserveutil.Handle(out, h.serveMapPage),
		"/player/search":  httpserveutil.Handle(out, h.serveSearchPage),
		"/player/results": httpserveutil.Handle(out, h.serveSearchResultsPage),
		"/player":         httpserveutil.Handle(out, h.servePlayerPage),
		"/results":        httpserveutil.Handle(out, h.serveResultsPage),
	}
}

func NewFlagSet(prog string) *flag.FlagSet {
	f := flag.NewFlagSet(prog, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = nil

	return f
}

func ParseArgs(flags *flag.FlagSet, args []string, stderr io.Writer, usage string) (bool, error) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, usage)
			return false, nil
		}

		return false, fmt.Errorf("argument parsing failure: %w\n\n%s", err, usage)
	}

	return true, nil
}

type PageTemplates struct {
	index         *template.Template
	completions   *template.Template
	classmap      *template.Template
	search        *template.Template
	results       *template.Template
	playerResults *template.Template
	player        *template.Template
}

func parseTemplates() (PageTemplates, error) {
	pt := PageTemplates{}

	groups := []templateutil.TemplateGroup{
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/index.html",
			},
			Add: func(t *template.Template) { pt.index = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/completions.html",
			},
			Add: func(t *template.Template) { pt.completions = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/class-map.html",
				"static/templates/results-table.html",
			},
			Add: func(t *template.Template) { pt.classmap = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/search.html",
			},
			Add: func(t *template.Template) { pt.search = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/results.html",
			},
			Add: func(t *template.Template) { pt.results = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/player-results.html",
				"static/templates/results-table.html",
				"static/templates/filters/class.html",
				"static/templates/filters/tiers.html",
				"static/templates/filters/zone-types.html",
			},
			Add: func(t *template.Template) { pt.playerResults = t },
		},
		{
			Files: []string{
				"static/templates/base.html",
				"static/templates/pages/player.html",
				"static/templates/results-table.html",
			},
			Add: func(t *template.Template) { pt.player = t },
		},
	}

	if err := templateutil.ParseFS(staticFS, groups); err != nil {
		return pt, fmt.Errorf("parse templates: %w", err)
	}

	return pt, nil
}
