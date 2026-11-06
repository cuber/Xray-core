package extension

import (
	"context"

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

func ObservatoryType() interface{} {
	return (*Observatory)(nil)
}
