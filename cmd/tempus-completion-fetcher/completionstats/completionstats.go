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

		points := pointValues[k]

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
	pointValues = map[pointValueKey]uint16{
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
)

func (a *mapClassAggregator) aggregate(zones []completionstore.PlayerClassZoneResult, mapClassStats completionstore.MapClassStats) completionstore.PlayerClassMapStats {
	a.reset()

	tierPointsLeft := mapClassStats.TierPointsTotal

	for _, zone := range zones {
		k := pointValueKey{
			Tier:     zone.Tier,
			ZoneType: zone.ZoneType,
		}
		points := pointValues[k]

		tierPointsLeft[zone.Tier-1] -= points

		a.pointsAcquired += points
		a.completed++
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
