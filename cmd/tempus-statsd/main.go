package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstats"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/cmd/tempus-completion-fetcher/rqlitecompletionstore"
	"tempus-completion/cmd/tempus-statsd/httpserveutil"
	"tempus-completion/cmd/tempus-statsd/templateutil"
	"tempus-completion/tempushttp"
	"tempus-completion/tempushttprpc"
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

	flags.StringVar(&rqliteaddr, "rqlite-address", "", "")

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

	// ctx := context.Background()
	// ctx, cancel := signal.NotifyContext(ctx, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM)
	// defer cancel()

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

	if err := http.ListenAndServe(":9876", mux); err != nil {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

type Store interface {
	GetPlayerResults(ctx context.Context, playerID uint64) ([]completionstore.PlayerClassZoneResult, bool, error)
	GetPlayerBySteamID(ctx context.Context, steamID string) (uint64, bool, error)
	GetPlayerClassZoneResults(ctx context.Context, playerID uint64, zoneTypes []string, tiers, classes []uint8) ([]completionstore.PlayerClassZoneResult, bool, error)
	GetPlayerMapZones(ctx context.Context, playerID, mapID uint64) (*completionstore.MapZones, bool, error)
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
	if err := h.templates.index.Execute(w, nil); err != nil {
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

		addr := fmt.Sprintf("/completions?playerid=%d", playerID)

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

	ctx := r.Context()

	mapZones, ok, err := h.store.GetPlayerMapZones(ctx, playerID, mapID)
	if err != nil {
		return httpserveutil.InternalError(w, "get completions: %w", err)
	}

	if !ok {
		if err := h.templates.index.Execute(w, nil); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}

		return nil
	}

	type pageData struct {
		Completions []completionstore.PlayerMapClassZoneCompletion
		MapName     string
		PlayerID    uint64
		Class       string
	}

	d := pageData{
		PlayerID: playerID,
		Class:    class,
		MapName:  mapZones.MapName,
	}

	switch class {
	case "soldier":
		d.Completions = mapZones.Soldier
	case "demoman":
		d.Completions = mapZones.Demoman
	}

	sort.Slice(d.Completions, func(i, j int) bool {
		pi := zoneTypePriorities[d.Completions[i].ZoneType]
		pj := zoneTypePriorities[d.Completions[j].ZoneType]

		if pi != pj {
			return pi < pj
		}

		return d.Completions[i].Tier < d.Completions[j].Tier
	})

	if err := h.templates.classmap.Execute(w, d); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

func (h *Handler) serveResultsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	pid := q.Get("playerid")
	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID: %w", err)
	}

	// class := q.Get("class")

	ctx := r.Context()

	results, ok, err := h.store.GetPlayerResults(ctx, playerID)
	if err != nil {
		return httpserveutil.InternalError(w, "get completions: %w", err)
	}

	if !ok {
		if err := h.templates.index.Execute(w, nil); err != nil {
			return fmt.Errorf("execute template: %w", err)
		}

		return nil
	}

	type pageData struct {
		Results  []completionstore.PlayerClassZoneResult
		PlayerID uint64
	}

	d := pageData{
		PlayerID: playerID,
		Results:  results,
	}

	// switch class {
	// case "soldier":
	// 	d.Completions = mapZones.Soldier
	// case "demoman":
	// 	d.Completions = mapZones.Demoman
	// }

	// sort.Slice(d.Completions, func(i, j int) bool {
	// 	pi := zoneTypePriorities[d.Completions[i].ZoneType]
	// 	pj := zoneTypePriorities[d.Completions[j].ZoneType]

	// 	if pi != pj {
	// 		return pi < pj
	// 	}

	// 	return d.Completions[i].Tier < d.Completions[j].Tier
	// })

	if err := h.templates.playerResults.Execute(w, d); err != nil {
		return fmt.Errorf("execute template: %w", err)
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
	sortFuncs = map[sortFuncsKey]SortFunc{
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
)

type SortFunc func([]completionstats.PlayerMapResultStats)

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

	var includeTiers completionstore.Bitmask

	tiers := q["tier"]

	if len(tiers) == 0 {
		tiers = []string{"t1", "t2", "t3", "t4", "t5", "t6"}
	}

	queryTiers := make([]uint8, 0, len(tiers))

	var pf pageFilters

	for _, v := range tiers {
		switch v {
		case "t1":
			queryTiers = append(queryTiers, 1)
			includeTiers = completionstore.Set(includeTiers, completionstore.T1)
			pf.Tier1Checked = true
		case "t2":
			queryTiers = append(queryTiers, 2)
			includeTiers = completionstore.Set(includeTiers, completionstore.T2)
			pf.Tier2Checked = true
		case "t3":
			queryTiers = append(queryTiers, 3)
			includeTiers = completionstore.Set(includeTiers, completionstore.T3)
			pf.Tier3Checked = true
		case "t4":
			queryTiers = append(queryTiers, 4)
			includeTiers = completionstore.Set(includeTiers, completionstore.T4)
			pf.Tier4Checked = true
		case "t5":
			queryTiers = append(queryTiers, 5)
			includeTiers = completionstore.Set(includeTiers, completionstore.T5)
			pf.Tier5Checked = true
		case "t6":
			queryTiers = append(queryTiers, 6)
			includeTiers = completionstore.Set(includeTiers, completionstore.T6)
			pf.Tier6Checked = true
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

	classes := q["class"]
	sortType := q.Get("sort")

	sfk := sortFuncsKey{
		sortType: sortType,
	}

	pf.Sort = sortType

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

	if sf, ok := sortFuncs[sfk]; ok {
		sf(stats)
	}
	measurement := q.Get("measurement")
	switch measurement {
	case "zones-finished-percentage", "points-finished-percentage":
	default:
		measurement = "zones-finished-percentage"
	}

	pf.Measurement = measurement

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

	return nil
}

func (h *Handler) Routes(out io.Writer) map[string]http.Handler {
	return map[string]http.Handler{
		"/":               httpserveutil.Handle(out, h.serveIndexPage),
		"/completions":    httpserveutil.Handle(out, h.serveCompletionsPage),
		"/map":            httpserveutil.Handle(out, h.serveMapPage),
		"/player/search":  httpserveutil.Handle(out, h.serveSearchPage),
		"/player/results": httpserveutil.Handle(out, h.serveSearchResultsPage),
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
			},
			Add: func(t *template.Template) { pt.playerResults = t },
		},
	}

	if err := templateutil.ParseFS(staticFS, groups); err != nil {
		return pt, fmt.Errorf("parse templates: %w", err)
	}

	return pt, nil
}
