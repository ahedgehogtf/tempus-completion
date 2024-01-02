package tempushttp

type GetDetailedMapListResponse []DetailedMapListMap

type DetailedMapListMap struct {
	ID         int                       `json:"id"`
	Name       string                    `json:"name"`
	ZoneCounts DetailedMapListZoneCounts `json:"zone_counts"`
	Authors    []DetailedMapListAuthor   `json:"authors"`
	TierInfo   DetailedMapListTierInfo   `json:"tier_info"`
	Videos     DetailedMapListVideos     `json:"videos"`
}

type DetailedMapListZoneCounts struct {
	Checkpoint int `json:"checkpoint"`
	BonusEnd   int `json:"bonus_end"`
	Linear     int `json:"linear"`
	Bonus      int `json:"bonus"`
	MapEnd     int `json:"map_end"`
	Map        int `json:"map"`
	Trick      int `json:"trick"`
	Misc       int `json:"misc"`
	Special    int `json:"special"`
	Course     int `json:"course"`
	CourseEnd  int `json:"course_end"`
}

type DetailedMapListAuthor struct {
	MapID int    `json:"map_id"`
	Name  string `json:"name"`
	ID    int    `json:"id"`
}

type DetailedMapListTierInfo struct {
	Soldier int `json:"3"`
	Demoman int `json:"4"`
}

type DetailedMapListVideos struct {
	Soldier string `json:"soldier"`
	Demoman string `json:"demoman"`
}

type ZoneType string

const (
	ZoneTypeTrick  ZoneType = "trick"
	ZoneTypeCourse ZoneType = "course"
	ZoneTypeBonus  ZoneType = "bonus"
	ZoneTypeMap    ZoneType = "map"
)

type ClassType uint8

const (
	ClassTypeSoldier ClassType = 3
	ClassTypeDemoman ClassType = 4
)

type GetPlayerZoneClassCompletionResponse struct {
	ZoneInfo       ZoneInfo         `json:"zone_info"`
	TierInfo       TierInfo         `json:"tier_info"`
	CompletionInfo CompletionInfo   `json:"completion_info"`
	Result         CompletionResult `json:"result"`
}
type ZoneInfo struct {
	ID         int    `json:"id"`
	MapID      uint64 `json:"map_id"`
	Zoneindex  int    `json:"zoneindex"`
	CustomName string `json:"custom_name"`
	Type       string `json:"type"`
}
type TierInfo struct {
	Soldier int `json:"3"`
	Demoman int `json:"4"`
}
type CompletionInfo struct {
	Soldier int `json:"soldier"`
	Demoman int `json:"demoman"`
}

type PlayerZoneClassCompletionServerInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type PlayerZoneClassCompletionDemoInfo struct {
	ID         int                                 `json:"id"`
	StartTick  int                                 `json:"start_tick"`
	EndTick    int                                 `json:"end_tick"`
	URL        string                              `json:"url"`
	ServerInfo PlayerZoneClassCompletionServerInfo `json:"server_info"`
}
type PlayerZoneClassCompletionPlayerInfo struct {
	ID      int    `json:"id"`
	Steamid string `json:"steamid"`
	Name    string `json:"name"`
}

type CompletionResult struct {
	ZoneID     int                                 `json:"zone_id"`
	Class      int                                 `json:"class"`
	DemoInfo   PlayerZoneClassCompletionDemoInfo   `json:"demo_info"`
	UserID     int                                 `json:"user_id"`
	SteamID    string                              `json:"steamid"`
	PlayerInfo PlayerZoneClassCompletionPlayerInfo `json:"player_info"`
	ID         int                                 `json:"id"`
	Duration   float64                             `json:"duration"`
	Date       float64                             `json:"date"`
	Name       string                              `json:"name"`
	Rank       int                                 `json:"rank"`
}

type PlayersAndMapsSearchResponse struct {
	Players []PlayersAndMapsSearchPlayer `json:"players"`
	Maps    []PlayersAndMapsSearchMap    `json:"maps"`
}

type PlayersAndMapsSearchPlayer struct {
	SteamID string `json:"steamid"`
	ID      uint64 `json:"id"`
	Name    string `json:"name"`
}

type PlayersAndMapsSearchMap struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type ZoneRecordsResults struct {
	Soldier []CompletionResult `json:"soldier"`
	Demoman []CompletionResult `json:"demoman"`
}

type ZoneRecordsResponse struct {
	ZoneInfo       ZoneInfo           `json:"zone_info"`
	TierInfo       TierInfo           `json:"tier_info"`
	CompletionInfo CompletionInfo     `json:"completion_info"`
	Results        ZoneRecordsResults `json:"results"`
}

type PlayerStatsPlayerInfo struct {
	ID          uint64  `json:"id"`
	SteamID     string  `json:"steamid"`
	Name        string  `json:"name"`
	FirstSeen   float64 `json:"first_seen"`
	LastSeen    float64 `json:"last_seen"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
}

type PlayerStatsOverallRankInfo struct {
	Points      float64 `json:"points"`
	Rank        uint32  `json:"rank"`
	TotalRanked uint32  `json:"total_ranked"`
}

type PlayerStatsClassRankInfoClass struct {
	Points      float64 `json:"points"`
	Rank        uint32  `json:"rank"`
	TotalRanked uint32  `json:"total_ranked"`
	Title       string  `json:"title"`
}

type PlayerStatsClassRankInfo struct {
	Soldier PlayerStatsClassRankInfoClass `json:"3"`
	Demoman PlayerStatsClassRankInfoClass `json:"4"`
}

// TODO: parse more
type GetPlayerStatsResponse struct {
	PlayerInfo      PlayerStatsPlayerInfo      `json:"player_info"`
	OverallRankInfo PlayerStatsOverallRankInfo `json:"rank_info"`
	ClassRankInfo   PlayerStatsClassRankInfo   `json:"class_rank_info"`
}
