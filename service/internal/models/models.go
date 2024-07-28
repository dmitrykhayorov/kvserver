package models

type Heartbeat struct {
	Id         string
	Addr       string
	LeaderAddr string
	ReplFactor int
	Cluster    []string
}

type Data struct {
	Uuid  string            `json:"uuid"`
	Value map[string]string `json:"data"`
}
