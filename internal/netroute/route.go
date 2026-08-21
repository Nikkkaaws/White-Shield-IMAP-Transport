package netroute

import "net"

// Selection identifies the host interface used for transport-underlay sockets.
type Selection struct {
	Index   uint32
	Name    string
	LocalIP net.IP
}

func Resolve(spec string) (Selection, error) {
	return resolve(spec)
}
