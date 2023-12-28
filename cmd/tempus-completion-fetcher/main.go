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
	"tempus-completion/cmd/tempus-completion-fetcher/completionstats"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/cmd/tempus-completion-fetcher/rqlitecompletionstore"
	"tempus-completion/tempushttp"
	"tempus-completion/tempushttprpc"
	"syscall"
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
	InsertMaps(ctx context.Context, maps *completionstore.MapList) error
	GetMaps(ctx context.Context) (*completionstore.MapList, error)
	GetStalePlayers(ctx context.Context) ([]completionstore.Player, error)
	GetStaleRawCompletions(ctx context.Context) (map[uint64]completionstore.RawPlayerCompletions, error)
	InsertPlayerResults(ctx context.Context, results map[uint64][]completionstore.CompletionResult) error
	InsertTransformedPlayerData(ctx context.Context, playerID uint64, data completionstore.TransformedPlayerData) error
}

type Fetcher struct {
	client *tempushttprpc.Client
	store  Store

	maps *completionstore.MapList

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

	fmt.Fprintf(f.stdout, "inserted %d maps\n", len(response))

	return nil
}

func (f *Fetcher) fetchCompletions(ctx context.Context, players []completionstore.Player) (map[uint64][]completionstore.CompletionResult, error) {
	classes := [2]tempushttp.ClassType{
		tempushttp.ClassTypeSoldier,
		tempushttp.ClassTypeDemoman,
	}

	estsize := len(players) * len(classes) * 714 * 6

	in := make(chan tempushttprpc.GetPlayerZoneClassCompletionData, estsize)
	out := make(chan completionstore.CompletionResult, estsize)
	defer close(out)

	g, ctx := errgroup.WithContext(ctx)

	for i := 0; i < 8; i++ {
		g.Go(func() error {
			for data := range in {
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				response, err := f.client.GetPlayerZoneClassCompletion(ctx, data)
				cancel()

				if err != nil {
					return fmt.Errorf("get completion: %w", err)
				}

				result := completionstore.CompletionResult{
					PlayerID:  data.PlayerID,
					MapName:   data.MapName,
					ZoneType:  data.ZoneType,
					ZoneIndex: data.ZoneIndex,
					Class:     data.Class,
					Response:  response,
				}

				out <- result
			}

			return nil
		})
	}

	var count uint16

	start := time.Now()

	for _, c := range classes {
	maploop:
		for _, m := range f.maps.Response {
			switch c {
			case tempushttp.ClassTypeDemoman:
				if m.TierInfo.Demoman == 0 {
					continue maploop
				}
			case tempushttp.ClassTypeSoldier:
				if m.TierInfo.Soldier == 0 {
					continue maploop
				}
			}

			for _, p := range players {
				for i := 0; i < m.ZoneCounts.Bonus; i++ {
					data := tempushttprpc.GetPlayerZoneClassCompletionData{
						MapName:   m.Name,
						ZoneType:  tempushttp.ZoneTypeBonus,
						ZoneIndex: uint8(i + 1),
						PlayerID:  p.PlayerID,
						Class:     c,
					}

					in <- data
					count++
				}

				for i := 0; i < m.ZoneCounts.Map; i++ {
					data := tempushttprpc.GetPlayerZoneClassCompletionData{
						MapName:   m.Name,
						ZoneType:  tempushttp.ZoneTypeMap,
						ZoneIndex: uint8(i + 1),
						PlayerID:  p.PlayerID,
						Class:     c,
					}

					in <- data
					count++
				}

				for i := 0; i < m.ZoneCounts.Course; i++ {
					data := tempushttprpc.GetPlayerZoneClassCompletionData{
						MapName:   m.Name,
						ZoneType:  tempushttp.ZoneTypeCourse,
						ZoneIndex: uint8(i + 1),
						PlayerID:  p.PlayerID,
						Class:     c,
					}

					in <- data
					count++
				}
			}
		}
	}
	fmt.Fprintf(f.stdout, "fetching %d completions\n", count)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	done := ctx.Done()

	completions := make(map[uint64][]completionstore.CompletionResult, len(players))

	for i := uint16(0); i < count; i++ {
		if i%50 == 0 {
			fmt.Fprintf(f.stdout, "processed %d completions\n", i)
		}

		select {
		case <-done:
			return nil, ctx.Err()
		case r := <-out:

			responses, ok := completions[r.PlayerID]
			if !ok {
				responses = make([]completionstore.CompletionResult, 0, 1200)
			}

			responses = append(responses, r)
			completions[r.PlayerID] = responses
		}
	}

	close(in)

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("errgroup: %w", err)
	}

	elapsed := time.Since(start)

	fmt.Fprintf(f.stdout, "found %d completions in %s seconds\n", count, elapsed)

	return completions, nil
}

func (f *Fetcher) Run(ctx context.Context) error {
	if time.Since(f.maps.Updated) > 24*time.Hour {
		fmt.Fprintln(f.stdout, "maps data out-of-date, updating")

		if err := f.UpdateMaps(ctx); err != nil {
			return fmt.Errorf("update maps: %w", err)
		}
	}

	fmt.Fprintln(f.stdout, "updating raw player completions")

	if err := f.updateRawPlayerCompletions(ctx); err != nil {
		return fmt.Errorf("update raw player completions: %w", err)
	}

	fmt.Fprintln(f.stdout, "finished updating raw player completions")

	if err := f.transformRawPlayerCompletions(ctx); err != nil {
		return fmt.Errorf("transform raw player completions: %w", err)
	}

	return nil
}

func (f *Fetcher) updateRawPlayerCompletions(ctx context.Context) error {
	players, err := f.store.GetStalePlayers(ctx)
	if err != nil {
		return fmt.Errorf("get stale players: %w", err)
	}

	fmt.Fprintf(f.stdout, "found %d stale players\n", len(players))

	if len(players) == 0 {
		return nil
	}

	completions, err := f.fetchCompletions(ctx, players)
	if err != nil {
		return fmt.Errorf("fetch completions: %w", err)
	}

	if err := f.store.InsertPlayerResults(ctx, completions); err != nil {
		return fmt.Errorf("insert player completions: %w", err)
	}

	return nil
}

func (f *Fetcher) transformRawPlayerCompletions(ctx context.Context) error {
	rawPlayerCompletions, err := f.store.GetStaleRawCompletions(ctx)
	if err != nil {
		return fmt.Errorf("get stale raw completions: %w", err)
	}

	fmt.Fprintf(f.stdout, "found %d stale raw players\n", len(rawPlayerCompletions))

	for playerID, completions := range rawPlayerCompletions {
		data := completionstats.ParsePlayer(completions)
		if err := f.store.InsertTransformedPlayerData(ctx, playerID, data); err != nil {
			return fmt.Errorf("insert transformed data: %w", err)
		}
	}

	return nil
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

	f := &Fetcher{
		client: client,
		store:  store,
		maps:   list,
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

			if err := f.Run(ctx); err != nil {
				fmt.Fprintf(stdout, "error during iteration: %s\n", err)
				retries++
				timer.Reset(sleep * time.Duration(retries+1))
				continue
			}

			retries = 0

			fmt.Fprintf(stdout, "finished iteration %d, sleeping\n", i)

			timer.Reset(sleep)
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
