package resources

type AgentSnapshot struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Allocated int    `json:"allocated"`
	Used      int    `json:"used"`
	Waiting   bool   `json:"waiting"`
}

type SessionSnapshot struct {
	SessionID   string          `json:"session_id"`
	Allocated   int             `json:"allocated"`
	Used        int             `json:"used"`
	Unallocated int             `json:"unallocated"`
	Agents      []AgentSnapshot `json:"agents"`
}

type GlobalSnapshot struct {
	Limit       int `json:"limit"`
	Reserved    int `json:"reserved"`
	Allocated   int `json:"allocated"`
	Used        int `json:"used"`
	Unallocated int `json:"unallocated"`
}

type FileHandleSnapshot struct {
	Global   GlobalSnapshot    `json:"global"`
	Sessions []SessionSnapshot `json:"sessions"`
}
