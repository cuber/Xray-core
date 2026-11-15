package extension

import (
	"context"

	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/features"
	"google.golang.org/protobuf/proto"
)

type Observatory interface {
	features.Feature

	GetObservation(ctx context.Context) (proto.Message, error)
}

type BurstObservatory interface {
	Observatory
	Check(tag []string)
}

// ObservedOutbounds is implemented by an observatory that can tell whether it reports an outbound
// (Core spec019 M6): a subject of its selectors, or a track follower.
type ObservedOutbounds interface {
	Observes(tag string) bool
}

// ChangeNotifier is implemented by a feature whose changes can alter a decision published from it
// (Core spec027 TL-5): the observatories (a result recorded or removed, a force-down change, a relay
// status change) and the outbound manager (a handler added or removed). fn runs after the change,
// outside the notifier's locks; after cancel returns it is not called again.
type ChangeNotifier interface {
	OnChange(fn func()) (cancel func())
}

var relayChanges signal.Broadcast

// NotifyRelayChange is fired by Hy2 when a relay binding may have changed (Core spec027 D12): a burst
// observation marks a refused relay dead without any probe result.
func NotifyRelayChange() { relayChanges.Fire() }

// OnRelayChange subscribes to NotifyRelayChange.
func OnRelayChange(fn func()) (cancel func()) { return relayChanges.Subscribe(fn) }

func ObservatoryType() interface{} {
	return (*Observatory)(nil)
}
