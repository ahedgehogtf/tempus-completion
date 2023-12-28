package completionstore

import (
	"fmt"
	"tempus-completion/tempushttp"
	"time"
)

type Player struct {
	PlayerID uint64
}

type RawPlayerCompletions struct {
	PlayerID uint64
	Updated  time.Time
	Results  []CompletionResult
}

type CompletionResult struct {
	PlayerID  uint64                                           `json:"player_id"`
	MapName   string                                           `json:"map_name"`
	ZoneType  tempushttp.ZoneType                              `json:"zone_type"`
	ZoneIndex uint8                                            `json:"zone_index"`
	Class     tempushttp.ClassType                             `json:"class_type"`
	Response  *tempushttp.GetPlayerZoneClassCompletionResponse `json:"response"`
}

type MapList struct {
	Updated  time.Time
	Response tempushttp.GetDetailedMapListResponse
}

type PlayerMapClassZoneCompletion struct {
	ZoneType    tempushttp.ZoneType `json:"zone_type"`
	ZoneIndex   uint8               `json:"zone_index"`
	CustomName  string              `json:"custom_name"`
	Tier        uint8               `json:"tier"`
	Duration    time.Duration       `json:"duration"`
	Recorded    time.Time           `json:"recorded"`
	Rank        uint32              `json:"rank"`
	Completions uint32              `json:"completions"`
	Demo        string              `json:"demo"`
	StartTick   uint64              `json:"start_tick"`
	EndTick     uint64              `json:"end_tick"`
}

type PlayerMapAggregation struct {
	PlayerID uint64
	Fetched  time.Time
	Stats    []PlayerMapStats
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

type PlayerClassMapStats struct {
	TotalCompletionPercentage uint8     `json:"total_completion_percentage"`
	PointCompletionPercentage uint8     `json:"point_completion_percentage"`
	Tiers                     Bitmask   `json:"tiers"`
	IncompleteTiers           Bitmask   `json:"incomplete_tiers"`
	TotalPointsAvailable      uint16    `json:"total_points_available"`
	PointsAvailableByTier     [6]uint16 `json:"points_available_by_tier"`
}

type TransformedPlayerData struct {
	Maps     PlayerMapAggregation
	MapZones map[uint64]MapZones
}

type MapZones struct {
	MapName string                         `json:"map_name"`
	Soldier []PlayerMapClassZoneCompletion `json:"soldier"`
	Demoman []PlayerMapClassZoneCompletion `json:"demoman"`
}
