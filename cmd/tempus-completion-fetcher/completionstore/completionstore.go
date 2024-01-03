package completionstore

import (
	"fmt"
	"tempus-completion/tempushttp"
	"time"
)

type Zone struct {
	MapName   string
	MapID     uint64
	ZoneType  tempushttp.ZoneType
	ZoneIndex uint8
}

type MapList struct {
	Updated  time.Time
	Response tempushttp.GetDetailedMapListResponse
}

type PlayerMapStats struct {
	MapID   uint64              `json:"map_id"`
	MapName string              `json:"map_name"`
	Soldier PlayerClassMapStats `json:"soldier"`
	Demoman PlayerClassMapStats `json:"demoman"`
}

type Bitmask uint8

const (
	T1 Bitmask = 1 << iota
	T2
	T3
	T4
	T5
	T6
)

func IntToMask(i uint8) Bitmask {
	switch i {
	case 1:
		return T1
	case 2:
		return T2
	case 3:
		return T3
	case 4:
		return T4
	case 5:
		return T5
	case 6:
		return T6
	default:
		panic(fmt.Errorf("%d is not a valid tier", i))
	}
}

func Set(b, flag Bitmask) Bitmask {
	return b | flag
}

type StalePlayerMap struct {
	PlayerMap
	LatestUpdate time.Time
}

type PlayerMap struct {
	PlayerID uint64
	MapID    uint64
}

type PlayerClassMapStats struct {
	TotalCompletionPercentage uint8     `json:"total_completion_percentage"`
	PointCompletionPercentage uint8     `json:"point_completion_percentage"`
	Tiers                     Bitmask   `json:"tiers"`
	IncompleteTiers           Bitmask   `json:"incomplete_tiers"`
	TotalPointsAvailable      uint16    `json:"total_points_available"`
	PointsAvailableByTier     [6]uint16 `json:"points_available_by_tier"`
}

type PlayerClassZoneResult struct {
	MapID       uint64
	ZoneType    tempushttp.ZoneType
	ZoneIndex   uint8
	PlayerID    uint64
	Class       tempushttp.ClassType
	CustomName  string
	MapName     string
	Tier        uint8
	Updated     time.Time
	Rank        uint32
	Duration    time.Duration
	Date        time.Time
	Completions uint32
}

type MapClass struct {
	MapID uint64
	Class tempushttp.ClassType
}

type MapClassStats struct {
	ZoneCount       uint8     `json:"zone_count"`
	PointsTotal     uint16    `json:"points_total"`
	Tiers           Bitmask   `json:"tiers"`
	TierPointsTotal [6]uint16 `json:"tier_points_total"`
}

type ZoneClassInfo struct {
	MapID       uint64
	ZoneType    tempushttp.ZoneType
	ZoneIndex   uint8
	Class       tempushttp.ClassType
	MapName     string
	CustomName  string
	Tier        uint8
	Completions uint32
}

type MapClassStatsInfo struct {
	MapName string
	Stats   MapClassStats
}

type MapStatsInfo struct {
	MapName string
	Stats   MapStats
}

type MapStats struct {
	Demoman MapClassStats `json:"demoman"`
	Soldier MapClassStats `json:"soldier"`
}
