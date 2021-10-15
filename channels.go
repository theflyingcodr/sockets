package sockets

type Channel struct {
	id    string
	conns map[string]*connection
}

func NewChannel(id string) *Channel {
	r := &Channel{
		id:    id,
		conns: make(map[string]*connection, 0),
	}
	return r
}
