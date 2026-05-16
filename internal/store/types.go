package store

import "errors"

const (
	StateActive   = "active"
	StatePaused   = "paused"
	StateDisabled = "disabled"

	EventRuleEnabled   = "rule_enabled"
	EventRuleDisabled  = "rule_disabled"
	EventRulePaused    = "rule_paused"
	EventRuleResumed   = "rule_resumed"
	EventTrafficApply  = "traffic_applied"
	EventClientReset   = "client_rebaseline"
	EventClientMissing = "client_missing"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous match")
	ErrConflict  = errors.New("conflict")
	ErrSchema    = errors.New("schema validation failed")
	ErrRace      = errors.New("compare-and-swap update failed")
)

type ClientTraffic struct {
	ID        int64
	InboundID int64
	Enable    int64
	Email     string
	Up        int64
	Down      int64
	AllTime   int64
	Total     int64
}

type Rule struct {
	ID          int64
	Name        string
	InboundID   int64
	Email       string
	FactorPPM   int64
	State       string
	CreatedAt   int64
	UpdatedAt   int64
	ActivatedAt *int64
	PausedAt    *int64
	DisabledAt  *int64
	ClientCount int64
}

type RuleClient struct {
	RuleID      int64
	TrafficID   int64
	InboundID   int64
	Email       string
	LastUp      int64
	LastDown    int64
	LastAllTime int64
	RemUp       int64
	RemDown     int64
	RemAllTime  int64
	MissingAt   *int64
	UpdatedAt   int64
}

type ActiveRuleClient struct {
	Rule   Rule
	Client RuleClient
}

type Event struct {
	ID        int64
	RuleID    *int64
	EventType string
	Message   string
	CreatedAt int64
}

type EventFilter struct {
	Limit     int
	Email     string
	InboundID *int64
}

type ClientFilter struct {
	InboundID              *int64
	LimitedOnly            bool
	IncludeDisabledClients bool
}
