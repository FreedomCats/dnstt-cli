//go:build !windows && !linux

package netguard

// On other systems Control does nothing: there is no datapath there yet that takes all
// traffic.
const supported = false

const bindPerFamily = true

const (
	option4 = "IP_BOUND_IF"
	option6 = "IPV6_BOUND_IF"
)

func (g *Guard) setter() setter {
	return func(uintptr, bool, uint32) error { return nil }
}

// v6Only takes every socket as dual-stack here: Control does nothing anyway.
func v6Only(uintptr) (bool, error) { return false, nil }

// choose has nothing to choose here.
func choose(uint64) (Choice, error) { return Choice{}, nil }

// subscribe gives no events here: there is no point in watching the network while
// Control does nothing.
func subscribe(func(id uint64)) (func(), error) { return func() {}, nil }
