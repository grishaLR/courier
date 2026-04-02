package jetstream

import "encoding/json"

type Event struct {
	Did        string          `json:"did"`
	TimeUS     int64           `json:"time_us"`
	Kind       string          `json:"kind"`
	Commit     *Commit         `json:"commit,omitempty"`
	Identity   json.RawMessage `json:"identity,omitempty"`
	Account    json.RawMessage `json:"account,omitempty"`
}

type Commit struct {
	Rev        string          `json:"rev"`
	Operation  string          `json:"operation"`
	Collection string          `json:"collection"`
	RKey       string          `json:"rkey"`
	Record     json.RawMessage `json:"record,omitempty"`
	CID        string          `json:"cid,omitempty"`
}
