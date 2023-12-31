package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstats"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/cmd/tempus-completion-fetcher/rqlitecompletionstore"
	"tempus-completion/tempushttp"
	"tempus-completion/tempushttprpc"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

type Store interface {
	InsertSteamIDs(ctx context.Context, steamIDs map[string]uint64) error
	InsertMapStats(ctx context.Context, stats map[uint64]completionstore.MapStatsInfo) error
	GetAllZoneClassInfo(ctx context.Context) ([]completionstore.ZoneClassInfo, error)
	InsertZoneClassInfo(ctx context.Context, info []completionstore.ZoneClassInfo) error
	SetPlayerMapsProcessed(ctx context.Context, maps []completionstore.StalePlayerMap) error
	InsertPlayerMapStats(ctx context.Context, stats map[completionstore.PlayerMap]completionstore.PlayerMapStats) error
	GetStalePlayerMaps(ctx context.Context) ([]completionstore.StalePlayerMap, error)
	GetPlayerMapResults(ctx context.Context, playerMaps []completionstore.StalePlayerMap) (map[completionstore.PlayerMap][]completionstore.PlayerClassZoneResult, error)
	SetZonesFetched(ctx context.Context, zones []completionstore.Zone, t time.Time) error
	InsertPlayerClassZoneResults(ctx context.Context, results []completionstore.PlayerClassZoneResult) error
	GetStaleZones(ctx context.Context, t time.Time) ([]completionstore.Zone, error)
	InsertZones(ctx context.Context, zones map[completionstore.Zone]struct{}) error
	InsertMaps(ctx context.Context, maps *completionstore.MapList) error
	GetMaps(ctx context.Context) (*completionstore.MapList, error)
}

type Fetcher struct {
	client *tempushttprpc.Client
	store  Store

	maps  *completionstore.MapList
	maps2 map[completionstore.MapClass]completionstore.MapClassStatsInfo

	stdout io.Writer
}

func (f *Fetcher) UpdateMaps(ctx context.Context) error {
	response, err := f.client.GetDetailedMapList(ctx)
	if err != nil {
		return fmt.Errorf("get detailed map list: %w", err)
	}

	list := &completionstore.MapList{
		Updated:  time.Now(),
		Response: response,
	}

	if err := f.store.InsertMaps(ctx, list); err != nil {
		return fmt.Errorf("insert maps: %w", err)
	}

	f.maps = list

	const estzones = 3000

	zones := make(map[completionstore.Zone]struct{}, estzones)

	for _, r := range response {
		for i := 0; i < r.ZoneCounts.Bonus; i++ {
			zone := completionstore.Zone{
				MapID:     uint64(r.ID),
				MapName:   r.Name,
				ZoneType:  tempushttp.ZoneTypeBonus,
				ZoneIndex: uint8(i + 1),
			}

			zones[zone] = struct{}{}
		}

		for i := 0; i < r.ZoneCounts.Map; i++ {
			zone := completionstore.Zone{
				MapID:     uint64(r.ID),
				MapName:   r.Name,
				ZoneType:  tempushttp.ZoneTypeMap,
				ZoneIndex: uint8(i + 1),
			}

			zones[zone] = struct{}{}
		}

		for i := 0; i < r.ZoneCounts.Course; i++ {
			zone := completionstore.Zone{
				MapID:     uint64(r.ID),
				MapName:   r.Name,
				ZoneType:  tempushttp.ZoneTypeCourse,
				ZoneIndex: uint8(i + 1),
			}

			zones[zone] = struct{}{}
		}

		for i := 0; i < r.ZoneCounts.Trick; i++ {
			zone := completionstore.Zone{
				MapID:     uint64(r.ID),
				MapName:   r.Name,
				ZoneType:  tempushttp.ZoneTypeTrick,
				ZoneIndex: uint8(i + 1),
			}

			zones[zone] = struct{}{}
		}
	}

	if err := f.store.InsertZones(ctx, zones); err != nil {
		return fmt.Errorf("insert zones: %w", err)
	}

	fmt.Fprintf(f.stdout, "inserted %d maps\n", len(response))

	mapStats := make(map[uint64]completionstore.MapStatsInfo)

	for mc, mapClassStats := range f.maps2 {
		s := mapStats[mc.MapID]
		s.MapName = mapClassStats.MapName

		switch mc.Class {
		case tempushttp.ClassTypeDemoman:
			s.Stats.Demoman = completionstore.MapClassStats{
				ZoneCount:       mapClassStats.Stats.ZoneCount,
				PointsTotal:     mapClassStats.Stats.PointsTotal,
				Tiers:           mapClassStats.Stats.Tiers,
				TierPointsTotal: mapClassStats.Stats.TierPointsTotal,
			}
		case tempushttp.ClassTypeSoldier:
			s.Stats.Soldier = completionstore.MapClassStats{
				ZoneCount:       mapClassStats.Stats.ZoneCount,
				PointsTotal:     mapClassStats.Stats.PointsTotal,
				Tiers:           mapClassStats.Stats.Tiers,
				TierPointsTotal: mapClassStats.Stats.TierPointsTotal,
			}
		}

		mapStats[mc.MapID] = s
	}

	if err := f.store.InsertMapStats(ctx, mapStats); err != nil {
		return fmt.Errorf("insert map stats: %w", err)
	}

	return nil
}

func (f *Fetcher) Run(ctx context.Context) (bool, error) {
	if time.Since(f.maps.Updated) > 24*time.Hour {
		fmt.Fprintln(f.stdout, "maps data out-of-date, updating")

		if err := f.UpdateMaps(ctx); err != nil {
			return false, fmt.Errorf("update maps: %w", err)
		}
	}

	ok, err := f.updateRawPlayerCompletionsNew(ctx)
	if err != nil {
		return false, fmt.Errorf("update raw player completions: %w", err)
	}

	if ok {
		return true, nil
	}

	ok, err = f.transformRawPlayerCompletionsNew(ctx)
	if err != nil {
		return false, fmt.Errorf("transform raw player completions: %w", err)
	}

	return ok, nil
}

type zoneResults struct {
	Demoman            completionstore.ZoneClassInfo
	Soldier            completionstore.ZoneClassInfo
	PlayerClassResults []completionstore.PlayerClassZoneResult
	SteamIDs           map[string]uint64
}

func (f *Fetcher) fetchPlayerClassZoneResults(ctx context.Context, zones []completionstore.Zone) ([]zoneResults, error) {
	estsize := len(zones)

	in := make(chan completionstore.Zone, estsize)
	out := make(chan zoneResults, estsize)
	defer close(out)

	g, ctx := errgroup.WithContext(ctx)

	updated := time.Now()

	for i := 0; i < 8; i++ {
		g.Go(func() error {
			for data := range in {
				// change to 1 for fast zone refreshes
				const limit = 0

				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				response, err := f.client.GetZoneRecords(ctx, data.MapID, data.ZoneType, data.ZoneIndex, limit)
				cancel()

				if err != nil {
					return fmt.Errorf("get zone records: %w", err)
				}

				ns := len(response.Results.Soldier)
				nd := len(response.Results.Demoman)

				if ns != response.CompletionInfo.Soldier {
					fmt.Fprintf(f.stdout, "response expects %d soldier completions, found %d\n", response.CompletionInfo.Soldier, ns)

					ns = response.CompletionInfo.Soldier
				}

				if nd != response.CompletionInfo.Demoman {
					fmt.Fprintf(f.stdout, "response expects %d demoman completions, found %d\n", response.CompletionInfo.Demoman, nd)

					nd = response.CompletionInfo.Demoman
				}

				results := make([]completionstore.PlayerClassZoneResult, 0, ns+nd)

				steamIDs := make(map[string]uint64)

				mapID := response.ZoneInfo.MapID
				zoneType := tempushttp.ZoneType(response.ZoneInfo.Type)
				zoneIndex := uint8(response.ZoneInfo.Zoneindex)
				customName := response.ZoneInfo.CustomName

				for _, r := range response.Results.Soldier {
					result := completionstore.PlayerClassZoneResult{
						MapID:       mapID,
						ZoneType:    zoneType,
						ZoneIndex:   zoneIndex,
						PlayerID:    uint64(r.PlayerInfo.ID),
						Class:       tempushttp.ClassTypeSoldier,
						CustomName:  customName,
						MapName:     data.MapName,
						Tier:        uint8(response.TierInfo.Soldier),
						Updated:     updated,
						Rank:        uint32(r.Rank),
						Duration:    time.Second * time.Duration(r.Duration),
						Date:        time.Unix(int64(r.Date), 0),
						Completions: uint32(ns),
					}

					results = append(results, result)
					steamIDs[r.SteamID] = result.PlayerID
				}

				for _, r := range response.Results.Demoman {
					result := completionstore.PlayerClassZoneResult{
						MapID:       mapID,
						ZoneType:    zoneType,
						ZoneIndex:   zoneIndex,
						PlayerID:    uint64(r.PlayerInfo.ID),
						Class:       tempushttp.ClassTypeDemoman,
						CustomName:  customName,
						MapName:     data.MapName,
						Tier:        uint8(response.TierInfo.Demoman),
						Updated:     updated,
						Rank:        uint32(r.Rank),
						Duration:    time.Second * time.Duration(r.Duration),
						Date:        time.Unix(int64(r.Date), 0),
						Completions: uint32(nd),
					}

					results = append(results, result)
					steamIDs[r.SteamID] = result.PlayerID
				}

				zr := zoneResults{
					Demoman: completionstore.ZoneClassInfo{
						MapID:       mapID,
						MapName:     data.MapName,
						ZoneType:    zoneType,
						ZoneIndex:   zoneIndex,
						Class:       tempushttp.ClassTypeDemoman,
						CustomName:  customName,
						Tier:        uint8(response.TierInfo.Demoman),
						Completions: uint32(nd),
					},
					Soldier: completionstore.ZoneClassInfo{
						MapID:       mapID,
						MapName:     data.MapName,
						ZoneType:    zoneType,
						ZoneIndex:   zoneIndex,
						Class:       tempushttp.ClassTypeSoldier,
						CustomName:  customName,
						Tier:        uint8(response.TierInfo.Soldier),
						Completions: uint32(ns),
					},
					PlayerClassResults: results,
					SteamIDs:           steamIDs,
				}

				out <- zr
			}

			return nil
		})
	}

	start := time.Now()
	for _, z := range zones {
		in <- z
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	done := ctx.Done()

	results := make([]zoneResults, 0, len(zones))

loop:
	for i := 0; i < len(zones); i++ {
		select {
		case <-done:
			break loop
		case r := <-out:
			results = append(results, r)
		}
	}

	close(in)

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("errgroup: %w", err)
	}

	elapsed := time.Since(start)

	fmt.Fprintf(f.stdout, "found %d results in %s seconds\n", len(results), elapsed)

	return results, nil
}

func (f *Fetcher) updateRawPlayerCompletionsNew(ctx context.Context) (bool, error) {
	now := time.Now()

	threshold := now.Add(-1 * (24 * time.Hour))

	zones, err := f.store.GetStaleZones(ctx, threshold)
	if err != nil {
		return false, fmt.Errorf("get stale zones: %w", err)
	}

	fmt.Fprintf(f.stdout, "found %d stale zones\n", len(zones))

	if len(zones) == 0 {
		return false, nil
	}

	zoneResults, err := f.fetchPlayerClassZoneResults(ctx, zones)
	if err != nil {
		return false, fmt.Errorf("fetch player class zone results: %w", err)
	}

	if len(zoneResults) == 0 {
		return false, nil
	}

	results := make([]completionstore.PlayerClassZoneResult, 0, len(zoneResults)*5000)
	info := make([]completionstore.ZoneClassInfo, 0, len(zoneResults)*2)
	steamIDs := make(map[string]uint64, 10000)

	for _, r := range zoneResults {
		results = append(results, r.PlayerClassResults...)
		info = append(info, r.Demoman, r.Soldier)

		for steamID, playerID := range r.SteamIDs {
			steamIDs[steamID] = playerID
		}
	}

	if err := f.store.InsertPlayerClassZoneResults(ctx, results); err != nil {
		return false, fmt.Errorf("insert player class zone results: %w", err)
	}

	if err := f.store.InsertZoneClassInfo(ctx, info); err != nil {
		return false, fmt.Errorf("insert zone info: %w", err)
	}

	if err := f.store.InsertSteamIDs(ctx, steamIDs); err != nil {
		return false, fmt.Errorf("insert steam IDs: %w", err)
	}

	if err := f.store.SetZonesFetched(ctx, zones, now); err != nil {
		return false, fmt.Errorf("set zones fetched: %w", err)
	}

	// TODO incremental update
	zoneClassInfo, err := f.store.GetAllZoneClassInfo(ctx)
	if err != nil {
		return false, fmt.Errorf("get all zone class info: %w", err)
	}

	f.maps2 = completionstats.AggregateMapStats(zoneClassInfo)

	return true, nil
}

func (f *Fetcher) transformRawPlayerCompletionsNew(ctx context.Context) (bool, error) {
	stalePlayerMaps, err := f.store.GetStalePlayerMaps(ctx)
	if err != nil {
		return false, fmt.Errorf("get stale player maps: %w", err)
	}

	fmt.Fprintf(f.stdout, "found %d stale player maps\n", len(stalePlayerMaps))

	if len(stalePlayerMaps) == 0 {
		return false, nil
	}

	results, err := f.store.GetPlayerMapResults(ctx, stalePlayerMaps)
	if err != nil {
		return false, fmt.Errorf("get player map results: %w", err)
	}

	calculator := completionstats.MapStatCalculator{
		PlayerMapResults:  results,
		MapClassStatsInfo: f.maps2,
	}

	stats := calculator.Calculate()

	if err := f.store.InsertPlayerMapStats(ctx, stats); err != nil {
		return false, fmt.Errorf("insert player map stats: %w", err)
	}

	if err := f.store.SetPlayerMapsProcessed(ctx, stalePlayerMaps); err != nil {
		return false, fmt.Errorf("set player maps processed: %w", err)
	}

	return true, nil
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := NewFlagSet("fetcher")

	var rqliteaddr string
	var initialize bool

	flags.StringVar(&rqliteaddr, "rqlite-address", "", "")
	flags.BoolVar(&initialize, "initialize", false, "")

	ok, err := Parse(flags, args, stderr, "")
	if err != nil {
		return fmt.Errorf("parse args: %w", err)
	}

	if !ok {
		return nil
	}

	if rqliteaddr == "" {
		return fmt.Errorf("-rqlite-address must be set")
	}

	ctx := context.Background()
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGTERM)
	defer cancel()

	httpc := http.Client{}

	client := tempushttprpc.NewClient(httpc, "")

	store, err := rqlitecompletionstore.New(rqliteaddr)
	if err != nil {
		return fmt.Errorf("new completion store: %w", err)
	}

	if initialize {
		if err := store.CreateSchema(ctx); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}

	list, err := store.GetMaps(ctx)
	if err != nil {
		return fmt.Errorf("get maps: %w", err)
	}

	zoneClassInfo, err := store.GetAllZoneClassInfo(ctx)
	if err != nil {
		return fmt.Errorf("get all zone class info: %w", err)
	}

	f := &Fetcher{
		client: client,
		store:  store,
		maps:   list,
		maps2:  completionstats.AggregateMapStats(zoneClassInfo),
		stdout: stdout,
	}

	done := ctx.Done()

	var retries uint8

	timer := time.NewTimer(0)
	sleep := 60 * time.Second

	for i := 0; ; i++ {
		select {
		case <-done:
			fmt.Fprintln(stdout, "Received exit signal, shutting down")
			return nil
		case <-timer.C:
			fmt.Fprintf(stdout, "running iteration %d\n", i)

			ok, err := f.Run(ctx)
			if err != nil {
				fmt.Fprintf(stdout, "error during iteration: %s\n", err)
				retries++
				timer.Reset(sleep * time.Duration(retries+1))
				continue
			}

			retries = 0

			fmt.Fprintf(stdout, "finished iteration %d\n", i)

			if ok {
				timer.Reset(0)
			} else {
				timer.Reset(sleep)
			}
		}
	}
}

func NewFlagSet(prog string) *flag.FlagSet {
	f := flag.NewFlagSet(prog, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = nil

	return f
}

func Parse(flags *flag.FlagSet, args []string, stderr io.Writer, usage string) (bool, error) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, usage)
			return false, nil
		}

		return false, fmt.Errorf("argument parsing failure: %w\n\n%s", err, usage)
	}

	return true, nil
}
