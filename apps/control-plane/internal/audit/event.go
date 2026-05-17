package audit

import "github.com/google/uuid"

type Severity string

const (
	SeverityDebug    Severity = "DEBUG"
	SeverityInfo     Severity = "INFO"
	SeverityNotice   Severity = "NOTICE"
	SeverityWarning  Severity = "WARNING"
	SeverityError    Severity = "ERROR"
	SeverityCritical Severity = "CRITICAL"
)

type ActorType string

const (
	ActorUser     ActorType = "USER"
	ActorService  ActorType = "SERVICE"
	ActorSystem   ActorType = "SYSTEM"
	ActorExternal ActorType = "EXTERNAL"
)

type Actor struct {
	ID   uuid.UUID
	Type ActorType
}

type Event struct {
	TenantID        uuid.UUID
	Actor           Actor
	Action          string
	ResourceType    string
	ResourceID      string
	Severity        Severity
	Before          any
	After           any
	RequestID       uuid.UUID
	SourceIP        string
	UserAgent       string
	JWTClaimsDigest string
}
