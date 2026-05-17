package audit

import "github.com/google/uuid"

func nullableUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
