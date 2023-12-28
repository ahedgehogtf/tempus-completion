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
	ZoneTypeTrick      ZoneType = "trick"
	ZoneTypeCourse     ZoneType = "course"
	ZoneTypeBonus      ZoneType = "bonus"
	ZoneTypeCheckpoint ZoneType = "checkpoint"
	ZoneTypeMap        ZoneType = "map"
)

type ClassType uint8

const (
	ClassTypeSoldier ClassType = 3
	ClassTypeDemoman ClassType = 4
)

type GetPlayerZoneClassCompletionResponse struct {
	ZoneInfo       PlayerZoneClassCompletionZoneInfo       `json:"zone_info"`
	TierInfo       PlayerZoneClassCompletionTierInfo       `json:"tier_info"`
	CompletionInfo PlayerZoneClassCompletionCompletionInfo `json:"completion_info"`
	Result         PlayerZoneClassCompletionResult         `json:"result"`
}
type PlayerZoneClassCompletionZoneInfo struct {
	ID         int    `json:"id"`
	MapID      uint64 `json:"map_id"`
	Zoneindex  int    `json:"zoneindex"`
	CustomName string `json:"custom_name"`
	Type       string `json:"type"`
}
type PlayerZoneClassCompletionTierInfo struct {
	Soldier int `json:"3"`
	Demoman int `json:"4"`
}
type PlayerZoneClassCompletionCompletionInfo struct {
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
type PlayerZoneClassCompletionResult struct {
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
