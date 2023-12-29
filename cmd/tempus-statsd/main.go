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
	GetPlayerMapZones(ctx context.Context, playerID, mapID uint64) (*completionstore.MapZones, bool, error)
	GetCompletions(ctx context.Context, playerid uint64) (map[uint64]completionstore.PlayerMapStats, bool, error)
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

func (h *Handler) serveResultsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	name := q.Get("name")
	if name == "" {
		return httpserveutil.BadRequest(w, "must specify name")
	}

	ctx := r.Context()

	result, err := h.client.SearchPlayersAndMaps(ctx, name)
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
		return d.Completions[i].Tier < d.Completions[j].Tier
	})

	if err := h.templates.classmap.Execute(w, d); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	return nil
}

func SoldierIncludeTier(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return includeTiers&s.Soldier.Tiers != 0
}

func SoldierCheckIncomplete(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return s.Soldier.IncompleteTiers&includeTiers != 0
}

func DemomanIncludeTier(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return includeTiers&s.Demoman.Tiers != 0
}

func BothIncludeTier(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return DemomanIncludeTier(includeTiers, s) || SoldierIncludeTier(includeTiers, s)
}

func DemomanCheckIncomplete(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return s.Demoman.IncompleteTiers&includeTiers != 0
}

func BothCheckIncomplete(includeTiers completionstore.Bitmask, s completionstore.PlayerMapStats) bool {
	return DemomanCheckIncomplete(includeTiers, s) || SoldierCheckIncomplete(includeTiers, s)
}

func SortSoldierTierAscendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.IncompleteTiers < stats[j].Soldier.IncompleteTiers
	})
}

func SortDemomanTierAscendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.IncompleteTiers < stats[j].Demoman.IncompleteTiers
	})
}

func SortBothTierAscendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.IncompleteTiers < stats[j].Demoman.IncompleteTiers
		s := stats[i].Soldier.IncompleteTiers < stats[j].Soldier.IncompleteTiers
		return d && s
	})
}

func SortSoldierTierAscendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.Tiers < stats[j].Soldier.Tiers
	})
}

func SortDemomanTierAscendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.Tiers < stats[j].Demoman.Tiers
	})
}

func SortBothTierAscendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.Tiers < stats[j].Demoman.Tiers
		s := stats[i].Soldier.Tiers < stats[j].Soldier.Tiers
		return d && s
	})
}

func SortSoldierTierDescendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.IncompleteTiers > stats[j].Soldier.IncompleteTiers
	})
}

func SortDemomanTierDescendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.IncompleteTiers > stats[j].Demoman.IncompleteTiers
	})
}

func SortBothTierDescendingIncomplete(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.IncompleteTiers > stats[j].Demoman.IncompleteTiers
		s := stats[i].Soldier.IncompleteTiers > stats[j].Soldier.IncompleteTiers
		return d && s
	})
}

func SortSoldierTierDescendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.Tiers > stats[j].Soldier.Tiers
	})
}

func SortDemomanTierDescendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.Tiers > stats[j].Demoman.Tiers
	})
}

func SortBothTierDescendingAll(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d := stats[i].Demoman.Tiers > stats[j].Demoman.Tiers
		s := stats[i].Soldier.Tiers > stats[j].Soldier.Tiers
		return d && s
	})
}

func SortMapNameAscending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].MapName < stats[j].MapName
	})
}

func SortMapNameDescending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].MapName > stats[j].MapName
	})
}
func SortSoldierCompletionDescending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.TotalCompletionPercentage > stats[j].Soldier.TotalCompletionPercentage
	})
}

func SortSoldierCompletionAscending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Soldier.TotalCompletionPercentage < stats[j].Soldier.TotalCompletionPercentage
	})
}
func SortDemomanCompletionDescending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.TotalCompletionPercentage > stats[j].Demoman.TotalCompletionPercentage
	})
}

func SortDemomanCompletionAscending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].Demoman.TotalCompletionPercentage < stats[j].Demoman.TotalCompletionPercentage
	})
}

func SortBothCompletionDescending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.TotalCompletionPercentage
		d1 := stats[j].Demoman.TotalCompletionPercentage
		s0 := stats[i].Soldier.TotalCompletionPercentage
		s1 := stats[j].Soldier.TotalCompletionPercentage

		return max(d0, s0) > max(d1, s1)
	})
}

func max(a, b uint8) uint8 {
	if a > b {
		return a
	}

	return b
}

func SortBothCompletionAscending(stats []completionstore.PlayerMapStats) {
	sort.SliceStable(stats, func(i, j int) bool {
		d0 := stats[i].Demoman.TotalCompletionPercentage
		d1 := stats[j].Demoman.TotalCompletionPercentage
		s0 := stats[i].Soldier.TotalCompletionPercentage
		s1 := stats[j].Soldier.TotalCompletionPercentage

		return max(d0, s0) < max(d1, s1)
	})
}

type sortFuncsKey struct {
	sortType      string
	class         string
	hideCompleted bool
}

var (
	sortFuncs = map[sortFuncsKey]SortFunc{
		{sortType: "tier-ascending", class: "both", hideCompleted: false}:           SortBothTierAscendingAll,
		{sortType: "tier-ascending", class: "both", hideCompleted: true}:            SortBothTierAscendingIncomplete,
		{sortType: "tier-ascending", class: "soldier", hideCompleted: false}:        SortSoldierTierAscendingAll,
		{sortType: "tier-ascending", class: "soldier", hideCompleted: true}:         SortSoldierTierAscendingIncomplete,
		{sortType: "tier-ascending", class: "demoman", hideCompleted: false}:        SortDemomanTierAscendingAll,
		{sortType: "tier-ascending", class: "demoman", hideCompleted: true}:         SortDemomanTierAscendingIncomplete,
		{sortType: "tier-descending", class: "both", hideCompleted: false}:          SortBothTierDescendingAll,
		{sortType: "tier-descending", class: "both", hideCompleted: true}:           SortBothTierDescendingIncomplete,
		{sortType: "tier-descending", class: "soldier", hideCompleted: false}:       SortSoldierTierDescendingAll,
		{sortType: "tier-descending", class: "soldier", hideCompleted: true}:        SortSoldierTierDescendingIncomplete,
		{sortType: "tier-descending", class: "demoman", hideCompleted: false}:       SortDemomanTierDescendingAll,
		{sortType: "tier-descending", class: "demoman", hideCompleted: true}:        SortDemomanTierDescendingIncomplete,
		{sortType: "map-name-descending", class: "both", hideCompleted: false}:      SortMapNameDescending,
		{sortType: "map-name-descending", class: "both", hideCompleted: true}:       SortMapNameDescending,
		{sortType: "map-name-descending", class: "soldier", hideCompleted: false}:   SortMapNameDescending,
		{sortType: "map-name-descending", class: "soldier", hideCompleted: true}:    SortMapNameDescending,
		{sortType: "map-name-descending", class: "demoman", hideCompleted: false}:   SortMapNameDescending,
		{sortType: "map-name-descending", class: "demoman", hideCompleted: true}:    SortMapNameDescending,
		{sortType: "map-name-ascending", class: "both", hideCompleted: false}:       SortMapNameAscending,
		{sortType: "map-name-ascending", class: "both", hideCompleted: true}:        SortMapNameAscending,
		{sortType: "map-name-ascending", class: "soldier", hideCompleted: false}:    SortMapNameAscending,
		{sortType: "map-name-ascending", class: "soldier", hideCompleted: true}:     SortMapNameAscending,
		{sortType: "map-name-ascending", class: "demoman", hideCompleted: false}:    SortMapNameAscending,
		{sortType: "map-name-ascending", class: "demoman", hideCompleted: true}:     SortMapNameAscending,
		{sortType: "", class: "both", hideCompleted: false}:                         SortMapNameAscending,
		{sortType: "", class: "both", hideCompleted: true}:                          SortMapNameAscending,
		{sortType: "", class: "soldier", hideCompleted: false}:                      SortMapNameAscending,
		{sortType: "", class: "soldier", hideCompleted: true}:                       SortMapNameAscending,
		{sortType: "", class: "demoman", hideCompleted: false}:                      SortMapNameAscending,
		{sortType: "", class: "demoman", hideCompleted: true}:                       SortMapNameAscending,
		{sortType: "completion-ascending", class: "both", hideCompleted: false}:     SortBothCompletionAscending,
		{sortType: "completion-ascending", class: "both", hideCompleted: true}:      SortBothCompletionAscending,
		{sortType: "completion-ascending", class: "soldier", hideCompleted: false}:  SortSoldierCompletionAscending,
		{sortType: "completion-ascending", class: "soldier", hideCompleted: true}:   SortSoldierCompletionAscending,
		{sortType: "completion-ascending", class: "demoman", hideCompleted: false}:  SortDemomanCompletionAscending,
		{sortType: "completion-ascending", class: "demoman", hideCompleted: true}:   SortDemomanCompletionAscending,
		{sortType: "completion-descending", class: "both", hideCompleted: false}:    SortBothCompletionDescending,
		{sortType: "completion-descending", class: "both", hideCompleted: true}:     SortBothCompletionDescending,
		{sortType: "completion-descending", class: "soldier", hideCompleted: false}: SortSoldierCompletionDescending,
		{sortType: "completion-descending", class: "soldier", hideCompleted: true}:  SortSoldierCompletionDescending,
		{sortType: "completion-descending", class: "demoman", hideCompleted: false}: SortDemomanCompletionDescending,
		{sortType: "completion-descending", class: "demoman", hideCompleted: true}:  SortDemomanCompletionDescending,
	}
)

type SortFunc func([]completionstore.PlayerMapStats)
type IncludeFunc func(completionstore.Bitmask, completionstore.PlayerMapStats) bool

func (h *Handler) serveCompletionsPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	pid := q.Get("playerid")

	playerID, err := strconv.ParseUint(pid, 10, 64)
	if err != nil {
		return httpserveutil.BadRequest(w, "malformed playerID: %w", err)
	}

	// TODO: use bitmask for filters, too
	type pageFilters struct {
		Tier1Checked         bool
		Tier2Checked         bool
		Tier3Checked         bool
		Tier4Checked         bool
		Tier5Checked         bool
		Tier6Checked         bool
		SoldierChecked       bool
		DemomanChecked       bool
		HideCompletedChecked bool
		Sort                 string
	}

	var includeTiers completionstore.Bitmask

	tiers := q["tier"]

	if len(tiers) == 0 {
		tiers = []string{"t1", "t2", "t3", "t4", "t5", "t6"}
	}

	var pf pageFilters

	for _, v := range tiers {
		switch v {
		case "t1":
			includeTiers = completionstore.Set(includeTiers, completionstore.T1)
			pf.Tier1Checked = true
		case "t2":
			includeTiers = completionstore.Set(includeTiers, completionstore.T2)
			pf.Tier2Checked = true
		case "t3":
			includeTiers = completionstore.Set(includeTiers, completionstore.T3)
			pf.Tier3Checked = true
		case "t4":
			includeTiers = completionstore.Set(includeTiers, completionstore.T4)
			pf.Tier4Checked = true
		case "t5":
			includeTiers = completionstore.Set(includeTiers, completionstore.T5)
			pf.Tier5Checked = true
		case "t6":
			includeTiers = completionstore.Set(includeTiers, completionstore.T6)
			pf.Tier6Checked = true
		}
	}

	includeFuncs := make([]IncludeFunc, 0, 4)

	hideCompleted := q.Get("hide-completed") == "true"

	pf.HideCompletedChecked = hideCompleted

	classes := q["class"]
	sortType := q.Get("sort")

	sfk := sortFuncsKey{
		sortType:      sortType,
		hideCompleted: hideCompleted,
	}

	pf.Sort = sortType

	switch len(classes) {
	case 2, 0:
		sfk.class = "both"

		pf.SoldierChecked = true
		pf.DemomanChecked = true

		if len(tiers) != 6 {
			includeFuncs = append(includeFuncs, BothIncludeTier)
		}

		if hideCompleted {
			includeFuncs = append(includeFuncs, BothCheckIncomplete)
		}

	case 1:
		switch classes[0] {
		case "soldier":
			sfk.class = "soldier"
			pf.SoldierChecked = true

			if len(tiers) != 6 {
				includeFuncs = append(includeFuncs, SoldierIncludeTier)
			}

			if hideCompleted && len(classes) == 1 {
				includeFuncs = append(includeFuncs, SoldierCheckIncomplete)
			}

		case "demoman":
			sfk.class = "demoman"
			pf.DemomanChecked = true

			if len(tiers) != 6 {
				includeFuncs = append(includeFuncs, DemomanIncludeTier)
			}

			if hideCompleted && len(classes) == 1 {
				includeFuncs = append(includeFuncs, DemomanCheckIncomplete)
			}
		}
	default:
		return httpserveutil.BadRequest(w, "must specify 1 or 2 classes")
	}

	ctx := r.Context()

	stats, ok, err := h.store.GetCompletions(ctx, playerID)
	if err != nil {
		return httpserveutil.InternalError(w, "get completions: %w", err)
	}

	if !ok {
		return httpserveutil.NotFound(w, "Your player ID was not found; for now you must submit a record before we can show you completions!")
	}

	filtered := make([]completionstore.PlayerMapStats, 0, len(stats))

loop:
	for _, stat := range stats {
		for _, include := range includeFuncs {
			if !include(includeTiers, stat) {
				continue loop
			}
		}

		filtered = append(filtered, stat)
	}

	if sf, ok := sortFuncs[sfk]; ok {
		sf(filtered)
	}

	type pageData struct {
		PlayerCompletions []completionstore.PlayerMapStats
		PlayerID          uint64
		Filters           pageFilters
	}

	d := pageData{
		PlayerCompletions: filtered,
		PlayerID:          playerID,
		Filters:           pf,
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
		"/player/results": httpserveutil.Handle(out, h.serveResultsPage),
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
	index       *template.Template
	completions *template.Template
	classmap    *template.Template
	search      *template.Template
	results     *template.Template
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
	}

	if err := templateutil.ParseFS(staticFS, groups); err != nil {
		return pt, fmt.Errorf("parse templates: %w", err)
	}

	return pt, nil
}
