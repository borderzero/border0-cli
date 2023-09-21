package models

type ResultValue string

const (
	ResultSuccess ResultValue = "success"
	ResultDenied  ResultValue = "denied"
)

type SessionUpdate struct {
	SessionKey string
	Socket     *Socket
	UserData   string
}
