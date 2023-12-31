package completionstats

import (
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/tempushttp"
)

func AggregateMapStats(zones []completionstore.ZoneClassInfo) map[completionstore.MapClass]completionstore.MapClassStatsInfo {
	maps := make(map[completionstore.MapClass]completionstore.MapClassStatsInfo, 715)

	for _, info := range zones {
		mc := completionstore.MapClass{
			MapID: info.MapID,
			Class: info.Class,
		}

		stats, ok := maps[mc]
		if !ok {
			stats = completionstore.MapClassStatsInfo{
				MapName: info.MapName,
				Stats: completionstore.MapClassStats{
					ZoneCount:       0,
					PointsTotal:     0,
					Tiers:           0,
					TierPointsTotal: [6]uint16{},
				},
			}
		}

		stats.Stats.ZoneCount++

		k := pointValueKey{
			Tier:     info.Tier,
			ZoneType: info.ZoneType,
		}

		points := completionPointValues[k]

		stats.Stats.PointsTotal += points
		stats.Stats.TierPointsTotal[info.Tier-1] += points

		tiermask := completionstore.IntToMask(info.Tier)
		stats.Stats.Tiers = completionstore.Set(stats.Stats.Tiers, tiermask)

		maps[mc] = stats
	}

	return maps
}

type MapStatCalculator struct {
	PlayerMapResults  map[completionstore.PlayerMap][]completionstore.PlayerClassZoneResult
	MapClassStatsInfo map[completionstore.MapClass]completionstore.MapClassStatsInfo
}

func (c MapStatCalculator) Calculate() map[completionstore.PlayerMap]completionstore.PlayerMapStats {
	a := &mapClassAggregator{}

	stats := make(map[completionstore.PlayerMap]completionstore.PlayerMapStats, len(c.PlayerMapResults))

	soldier := make([]completionstore.PlayerClassZoneResult, 0, 6)
	demoman := make([]completionstore.PlayerClassZoneResult, 0, 6)

	for pm, results := range c.PlayerMapResults {
		s := completionstore.PlayerMapStats{
			MapID:   pm.MapID,
			MapName: results[0].MapName,
		}

		soldier = soldier[:0]
		demoman = demoman[:0]

		for _, r := range results {
			switch r.Class {
			case tempushttp.ClassTypeDemoman:
				demoman = append(demoman, r)
			case tempushttp.ClassTypeSoldier:
				soldier = append(soldier, r)
			}
		}

		mc := completionstore.MapClass{
			MapID: pm.MapID,
			Class: tempushttp.ClassTypeSoldier,
		}

		soldierMapStats, ok := c.MapClassStatsInfo[mc]
		if ok {
			s.Soldier = a.aggregate(soldier, soldierMapStats.Stats)

		}

		mc.Class = tempushttp.ClassTypeDemoman

		demomanMapStats, ok := c.MapClassStatsInfo[mc]
		if ok {
			s.Demoman = a.aggregate(demoman, demomanMapStats.Stats)
		}

		stats[pm] = s
	}

	return stats
}

type mapClassAggregator struct {
	pointsAcquired uint16
	completed      int
}

func (a *mapClassAggregator) reset() {
	a.pointsAcquired = 0
	a.completed = 0
}

type pointValueKey struct {
	Tier     uint8
	ZoneType tempushttp.ZoneType
}

var (
	completionPointValues = map[pointValueKey]uint16{
		{Tier: 1, ZoneType: tempushttp.ZoneTypeBonus}:  2,
		{Tier: 2, ZoneType: tempushttp.ZoneTypeBonus}:  5,
		{Tier: 3, ZoneType: tempushttp.ZoneTypeBonus}:  10,
		{Tier: 4, ZoneType: tempushttp.ZoneTypeBonus}:  20,
		{Tier: 5, ZoneType: tempushttp.ZoneTypeBonus}:  30,
		{Tier: 6, ZoneType: tempushttp.ZoneTypeBonus}:  50,
		{Tier: 1, ZoneType: tempushttp.ZoneTypeMap}:    10,
		{Tier: 2, ZoneType: tempushttp.ZoneTypeMap}:    20,
		{Tier: 3, ZoneType: tempushttp.ZoneTypeMap}:    30,
		{Tier: 4, ZoneType: tempushttp.ZoneTypeMap}:    50,
		{Tier: 5, ZoneType: tempushttp.ZoneTypeMap}:    100,
		{Tier: 6, ZoneType: tempushttp.ZoneTypeMap}:    200,
		{Tier: 1, ZoneType: tempushttp.ZoneTypeCourse}: 5,
		{Tier: 2, ZoneType: tempushttp.ZoneTypeCourse}: 10,
		{Tier: 3, ZoneType: tempushttp.ZoneTypeCourse}: 20,
		{Tier: 4, ZoneType: tempushttp.ZoneTypeCourse}: 30,
		{Tier: 5, ZoneType: tempushttp.ZoneTypeCourse}: 50,
		{Tier: 6, ZoneType: tempushttp.ZoneTypeCourse}: 100,
	}

	topTimesPointValues = map[pointValueKey][10]uint16{
		{Tier: 1, ZoneType: tempushttp.ZoneTypeBonus}:  {10, 7, 5, 4, 3, 3, 2, 2, 1, 1},
		{Tier: 2, ZoneType: tempushttp.ZoneTypeBonus}:  {20, 14, 10, 8, 7, 6, 5, 4, 3, 2},
		{Tier: 3, ZoneType: tempushttp.ZoneTypeBonus}:  {40, 28, 20, 16, 14, 12, 10, 8, 6, 4},
		{Tier: 4, ZoneType: tempushttp.ZoneTypeBonus}:  {60, 42, 30, 24, 21, 18, 15, 12, 9, 6},
		{Tier: 5, ZoneType: tempushttp.ZoneTypeBonus}:  {80, 56, 40, 32, 28, 24, 20, 16, 12, 8},
		{Tier: 6, ZoneType: tempushttp.ZoneTypeBonus}:  {100, 70, 50, 40, 35, 30, 25, 20, 15, 10},
		{Tier: 1, ZoneType: tempushttp.ZoneTypeMap}:    {200, 140, 100, 80, 70, 60, 50, 40, 30, 20},
		{Tier: 2, ZoneType: tempushttp.ZoneTypeMap}:    {250, 175, 125, 100, 87, 75, 62, 50, 37, 25},
		{Tier: 3, ZoneType: tempushttp.ZoneTypeMap}:    {300, 210, 150, 120, 105, 90, 75, 60, 45, 30},
		{Tier: 4, ZoneType: tempushttp.ZoneTypeMap}:    {350, 244, 175, 140, 122, 105, 87, 70, 52, 35},
		{Tier: 5, ZoneType: tempushttp.ZoneTypeMap}:    {400, 280, 200, 160, 140, 120, 100, 80, 60, 40},
		{Tier: 6, ZoneType: tempushttp.ZoneTypeMap}:    {500, 350, 250, 200, 175, 150, 125, 100, 75, 50},
		{Tier: 1, ZoneType: tempushttp.ZoneTypeCourse}: {100, 70, 50, 40, 35, 30, 25, 20, 15, 10},
		{Tier: 2, ZoneType: tempushttp.ZoneTypeCourse}: {150, 105, 75, 60, 52, 45, 37, 30, 22, 15},
		{Tier: 3, ZoneType: tempushttp.ZoneTypeCourse}: {200, 140, 100, 80, 70, 60, 50, 40, 30, 20},
		{Tier: 4, ZoneType: tempushttp.ZoneTypeCourse}: {250, 175, 125, 100, 87, 75, 62, 50, 37, 25},
		{Tier: 5, ZoneType: tempushttp.ZoneTypeCourse}: {300, 210, 150, 120, 105, 90, 75, 60, 45, 30},
		{Tier: 6, ZoneType: tempushttp.ZoneTypeCourse}: {400, 280, 200, 160, 140, 120, 100, 80, 60, 40},
	}
)

func (a *mapClassAggregator) aggregate(zones []completionstore.PlayerClassZoneResult, mapClassStats completionstore.MapClassStats) completionstore.PlayerClassMapStats {
	a.reset()

	tierPointsLeft := mapClassStats.TierPointsTotal

	for _, zone := range zones {
		a.completed++

		if zone.ZoneType == tempushttp.ZoneTypeTrick {
			continue
		}

		k := pointValueKey{
			Tier:     zone.Tier,
			ZoneType: zone.ZoneType,
		}
		points := completionPointValues[k]

		tierPointsLeft[zone.Tier-1] -= points

		a.pointsAcquired += points
	}

	var incompleteTiers completionstore.Bitmask

	for i, p := range tierPointsLeft {
		if p == 0 {
			continue
		}

		tiermask := completionstore.IntToMask(uint8(i + 1))
		incompleteTiers = completionstore.Set(incompleteTiers, tiermask)
	}

	stats := completionstore.PlayerClassMapStats{
		TotalCompletionPercentage: uint8((len(zones) * 100) / int(mapClassStats.ZoneCount)),
		PointCompletionPercentage: uint8((a.pointsAcquired * 100) / mapClassStats.PointsTotal),
		Tiers:                     mapClassStats.Tiers,
		IncompleteTiers:           incompleteTiers,
		TotalPointsAvailable:      mapClassStats.PointsTotal,
		PointsAvailableByTier:     tierPointsLeft,
	}

	return stats
}

func AggregateMapResultStats(results []completionstore.PlayerClassZoneResult, hideCompleted bool) []PlayerMapResultStats {
	maps := make(map[uint64]PlayerMapResultStats)

	for _, r := range results {
		if hideCompleted && r.Rank != 0 {
			continue
		}

		s, ok := maps[r.MapID]
		if !ok {
			s.MapID = r.MapID
			s.MapName = r.MapName
		}

		k := pointValueKey{
			Tier:     r.Tier,
			ZoneType: r.ZoneType,
		}

		points := completionPointValues[k]
		tiermask := completionstore.IntToMask(r.Tier)

		switch r.Class {
		case tempushttp.ClassTypeDemoman:
			s.Demoman.Tiers = completionstore.Set(s.Demoman.Tiers, tiermask)
			s.Demoman.PointsTotal += points
			s.Demoman.ZonesTotal++
			s.Demoman.CompletionsCount += r.Completions

			if s.Demoman.ZonesTotal == 1 || s.Demoman.LeastPopularCompletions > r.Completions {
				s.Demoman.LeastPopularCompletions = r.Completions
			}

			if s.Demoman.MostPopularCompletions < r.Completions {
				s.Demoman.MostPopularCompletions = r.Completions
			}

			if r.Rank != 0 {
				s.Demoman.PointsFinished += points
				s.Demoman.ZonesFinished++
			}

			s.Demoman.PointsFinishedPercentage = uint8((s.Demoman.PointsFinished * 100) / uint16(s.Demoman.PointsTotal))
			s.Demoman.ZonesFinishedPercentage = uint8(uint16(s.Demoman.ZonesFinished) * 100 / uint16(s.Demoman.ZonesTotal))

			s.Demoman.Results = append(s.Demoman.Results, r)
		case tempushttp.ClassTypeSoldier:
			s.Soldier.Tiers = completionstore.Set(s.Soldier.Tiers, tiermask)
			s.Soldier.PointsTotal += points
			s.Soldier.ZonesTotal++
			s.Soldier.CompletionsCount += r.Completions

			if s.Soldier.ZonesTotal == 1 || s.Soldier.LeastPopularCompletions > r.Completions {
				s.Soldier.LeastPopularCompletions = r.Completions
			}

			if s.Soldier.MostPopularCompletions < r.Completions {
				s.Soldier.MostPopularCompletions = r.Completions
			}

			if r.Rank != 0 {
				s.Soldier.PointsFinished += points
				s.Soldier.ZonesFinished++
			}

			s.Soldier.PointsFinishedPercentage = uint8((s.Soldier.PointsFinished * 100) / uint16(s.Soldier.PointsTotal))
			s.Soldier.ZonesFinishedPercentage = uint8(uint16(s.Soldier.ZonesFinished) * 100 / uint16(s.Soldier.ZonesTotal))

			s.Soldier.Results = append(s.Soldier.Results, r)
		}

		maps[s.MapID] = s
	}

	resultstats := make([]PlayerMapResultStats, 0, len(maps))

	for _, stat := range maps {
		resultstats = append(resultstats, stat)
	}

	return resultstats
}

type PlayerMapResultStats struct {
	MapID   uint64
	MapName string
	Demoman PlayerClassMapResultStats
	Soldier PlayerClassMapResultStats
}

type PlayerClassMapResultStats struct {
	PointsTotal              uint16
	ZonesTotal               uint8
	PointsFinished           uint16
	ZonesFinished            uint8
	PointsFinishedPercentage uint8
	ZonesFinishedPercentage  uint8
	MostPopularCompletions   uint32
	LeastPopularCompletions  uint32
	CompletionsCount         uint32
	Tiers                    completionstore.Bitmask
	Results                  []completionstore.PlayerClassZoneResult
}
