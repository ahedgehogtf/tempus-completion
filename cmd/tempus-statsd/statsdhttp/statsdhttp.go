package statsdhttp

type PlayerClassZoneResult struct {
	MapID       uint64 `json:"map_id"`
	ZoneType    string `json:"zone_type"`
	ZoneIndex   uint8  `json:"zone_index"`
	PlayerID    uint64 `json:"player_id"`
	Class       uint8  `json:"class"`
	CustomName  string `json:"custom_name"`
	MapName     string `json:"map_name"`
	Tier        uint8  `json:"tier"`
	Rank        uint32 `json:"rank"`
	Duration    int64  `json:"duration"`
	Date        int64  `json:"date"`
	Completions uint32 `json:"completions"`
}

type PlayerMapResultStats struct {
	MapID   uint64                               `json:"map_id"`
	MapName string                               `json:"map_name"`
	Demoman CompletionsPlayerClassMapResultStats `json:"demoman"`
	Soldier CompletionsPlayerClassMapResultStats `json:"soldier"`
}

type CompletionsPlayerClassMapResultStats struct {
	PointsTotal              uint16                  `json:"points_total"`
	ZonesTotal               uint8                   `json:"zones_total"`
	PointsFinished           uint16                  `json:"points_finished"`
	ZonesFinished            uint8                   `json:"zones_finished"`
	PointsFinishedPercentage uint8                   `json:"points_finished_percentage"`
	ZonesFinishedPercentage  uint8                   `json:"zones_finished_percentage"`
	MostPopularCompletions   uint32                  `json:"most_popular_completions"`
	LeastPopularCompletions  uint32                  `json:"least_popular_completions"`
	CompletionsCount         uint32                  `json:"completions_count"`
	Tiers                    uint8                   `json:"tier_mask"`
	Results                  []PlayerClassZoneResult `json:"results"`
}

type CompletionsResponse struct {
	Stats []PlayerMapResultStats `json:"stats"`
}

type ResultsResponse struct {
	Results []PlayerClassZoneResult `json:"results"`
}
