package completionstats

import (
	"sort"
	"tempus-completion/cmd/tempus-completion-fetcher/completionstore"
	"tempus-completion/tempushttp"
	"time"
)

func ParsePlayer(raw completionstore.RawPlayerCompletions) completionstore.TransformedPlayerData {
	const approximateMapCount = 714

	transformed := completionstore.TransformedPlayerData{
		Maps: completionstore.PlayerMapAggregation{
			PlayerID: raw.PlayerID,
			Fetched:  raw.Updated,
			Stats:    make([]completionstore.PlayerMapStats, 0, approximateMapCount),
		},
		MapZones: make(map[uint64]completionstore.MapZones, approximateMapCount),
	}

	for _, result := range raw.Results {
		mapID := result.Response.ZoneInfo.MapID
		zones, ok := transformed.MapZones[mapID]
		if !ok {
			zones = completionstore.MapZones{
				MapName: result.MapName,
				Soldier: make([]completionstore.PlayerMapClassZoneCompletion, 0, 6),
				Demoman: make([]completionstore.PlayerMapClassZoneCompletion, 0, 6),
			}
		}

		zone := completionstore.PlayerMapClassZoneCompletion{
			ZoneType:   result.ZoneType,
			ZoneIndex:  result.ZoneIndex,
			CustomName: result.Response.ZoneInfo.CustomName,
			Duration:   time.Second * time.Duration(result.Response.Result.Duration),
			Recorded:   time.Unix(int64(result.Response.Result.Date), 0),
			Rank:       uint32(result.Response.Result.Rank),
			Demo:       result.Response.Result.DemoInfo.URL,
			StartTick:  uint64(result.Response.Result.DemoInfo.StartTick),
			EndTick:    uint64(result.Response.Result.DemoInfo.EndTick),
		}

		switch result.Class {
		case tempushttp.ClassTypeDemoman:
			zone.Tier = uint8(result.Response.TierInfo.Demoman)
			if zone.Tier == 0 {
				continue
			}

			zone.Completions = uint32(result.Response.CompletionInfo.Demoman)
			zones.Demoman = append(zones.Demoman, zone)
		case tempushttp.ClassTypeSoldier:
			zone.Tier = uint8(result.Response.TierInfo.Soldier)
			if zone.Tier == 0 {
				continue
			}

			zone.Completions = uint32(result.Response.CompletionInfo.Soldier)
			zones.Soldier = append(zones.Soldier, zone)
		}

		transformed.MapZones[mapID] = zones
	}

	a := &mapClassAggregator{}

	for mapID, z := range transformed.MapZones {
		mapstats := completionstore.PlayerMapStats{
			MapID:   mapID,
			MapName: z.MapName,
		}

		if len(z.Soldier) > 0 {
			mapstats.Soldier = a.Aggregate(z.Soldier)
		}

		if len(z.Demoman) > 0 {
			mapstats.Demoman = a.Aggregate(z.Demoman)
		}

		transformed.Maps.Stats = append(transformed.Maps.Stats, mapstats)
	}

	sort.Slice(transformed.Maps.Stats, func(i, j int) bool {
		return transformed.Maps.Stats[i].MapName < transformed.Maps.Stats[j].MapName
	})

	return transformed
}

type mapClassAggregator struct {
	pointsAcquired  uint16
	pointsTotal     uint16
	completed       int
	incompleteTiers completionstore.Bitmask
	tiers           completionstore.Bitmask
	tierPoints      [6]uint16
}

func (a *mapClassAggregator) reset() {
	a.pointsAcquired = 0
	a.pointsTotal = 0
	a.completed = 0
	a.incompleteTiers = 0
	a.tiers = 0

	for i := range a.tierPoints {
		a.tierPoints[i] = 0
	}
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

func (a *mapClassAggregator) Aggregate(zones []completionstore.PlayerMapClassZoneCompletion) completionstore.PlayerClassMapStats {
	a.reset()

	for _, zone := range zones {
		if zone.Tier == 0 {
			continue
		}

		tiermask := completionstore.IntToMask(zone.Tier)
		a.tiers = completionstore.Set(a.tiers, tiermask)

		k := pointValueKey{
			Tier:     zone.Tier,
			ZoneType: zone.ZoneType,
		}
		points := pointValues[k]

		a.pointsTotal += points
		a.tierPoints[zone.Tier-1] += points

		if zone.Duration == 0 {
			a.incompleteTiers = completionstore.Set(a.incompleteTiers, tiermask)
			continue
		}

		a.pointsAcquired += points
		a.completed++
	}

	var stats completionstore.PlayerClassMapStats

	if a.pointsTotal == 0 {
		return stats
	}

	stats = completionstore.PlayerClassMapStats{
		TotalCompletionPercentage: uint8((a.completed * 100) / len(zones)),
		PointCompletionPercentage: uint8((a.pointsAcquired * 100) / a.pointsTotal),
		Tiers:                     a.tiers,
		IncompleteTiers:           a.incompleteTiers,
		TotalPointsAvailable:      a.pointsTotal,
		PointsAvailableByTier:     a.tierPoints,
	}

	return stats
}
