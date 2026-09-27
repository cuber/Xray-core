package outbound

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
)

// Manager is to manage all outbound handlers.
type Manager struct {
	access           sync.RWMutex
	defaultHandler   outbound.Handler
	taggedHandler    map[string]outbound.Handler
	untaggedHandlers []outbound.Handler
	running          bool
	closed           bool
	tagsCache        *sync.Map
	retiring         map[outbound.Handler]struct{}
}

// New creates a new Manager.
func New(ctx context.Context, config *proxyman.OutboundConfig) (*Manager, error) {
	m := &Manager{
		taggedHandler: make(map[string]outbound.Handler),
		tagsCache:     &sync.Map{},
		retiring:      make(map[outbound.Handler]struct{}),
	}
	return m, nil
}

// Type implements common.HasType.
func (m *Manager) Type() interface{} {
	return outbound.ManagerType()
}

// Start implements core.Feature
func (m *Manager) Start() error {
	m.access.Lock()
	defer m.access.Unlock()
	if m.closed {
		return errors.New("outbound manager is closed")
	}

	m.running = true

	for _, h := range m.taggedHandler {
		if err := h.Start(); err != nil {
			return err
		}
	}

	for _, h := range m.untaggedHandlers {
		if err := h.Start(); err != nil {
			return err
		}
	}

	return nil
}

// Close implements core.Feature
func (m *Manager) Close() error {
	m.access.Lock()
	m.running = false
	m.closed = true
	var handlers []outbound.Handler
	for _, h := range m.taggedHandler {
		handlers = append(handlers, h)
	}
	handlers = append(handlers, m.untaggedHandlers...)
	for h := range m.retiring {
		handlers = append(handlers, h)
	}
	m.access.Unlock()
	// Close can wait for chained dispatches which read this registry.
	var errs []error
	for _, h := range handlers {
		errs = append(errs, h.Close())
	}
	return errors.Combine(errs...)
}

// GetDefaultHandler implements outbound.Manager.
func (m *Manager) GetDefaultHandler() outbound.Handler {
	m.access.RLock()
	defer m.access.RUnlock()

	if m.defaultHandler == nil {
		return nil
	}
	return m.defaultHandler
}

// GetHandler implements outbound.Manager.
func (m *Manager) GetHandler(tag string) outbound.Handler {
	m.access.RLock()
	defer m.access.RUnlock()
	if handler, found := m.taggedHandler[tag]; found {
		return handler
	}
	return nil
}

// AddHandler implements outbound.Manager.
func (m *Manager) AddHandler(ctx context.Context, handler outbound.Handler) error {
	m.access.Lock()
	defer m.access.Unlock()
	if m.closed {
		return errors.New("outbound manager is closed")
	}

	m.tagsCache = &sync.Map{}

	tag := handler.Tag()
	if len(tag) > 0 {
		if _, found := m.taggedHandler[tag]; found {
			return errors.New("existing tag found: " + tag)
		}
	}
	if m.running {
		if err := handler.Start(); err != nil {
			return err
		}
	}
	if m.defaultHandler == nil {
		m.defaultHandler = handler
	}
	if len(tag) > 0 {
		m.taggedHandler[tag] = handler
	} else {
		m.untaggedHandlers = append(m.untaggedHandlers, handler)
	}

	return nil
}

// RemoveHandler implements outbound.Manager.
func (m *Manager) RemoveHandler(ctx context.Context, tag string) error {
	if tag == "" {
		return common.ErrNoClue
	}
	m.access.Lock()
	m.tagsCache = &sync.Map{}
	handler := m.taggedHandler[tag]
	delete(m.taggedHandler, tag)
	if m.defaultHandler != nil && m.defaultHandler.Tag() == tag {
		m.defaultHandler = nil
	}
	var retiring outbound.Retirement
	if optional, ok := handler.(outbound.RetiringHandler); ok {
		retiring = optional.Retirement()
	}
	if retiring != nil {
		m.retiring[handler] = struct{}{}
	}
	m.access.Unlock()
	if retiring != nil {
		done := retiring.Retire()
		if done == nil {
			m.access.Lock()
			delete(m.retiring, handler)
			m.access.Unlock()
		} else {
			go func() {
				<-done
				m.access.Lock()
				delete(m.retiring, handler)
				m.access.Unlock()
			}()
		}
	}

	return nil
}

// ListHandlers implements outbound.Manager.
func (m *Manager) ListHandlers(ctx context.Context) []outbound.Handler {
	m.access.RLock()
	defer m.access.RUnlock()

	response := make([]outbound.Handler, len(m.untaggedHandlers))
	copy(response, m.untaggedHandlers)

	for _, v := range m.taggedHandler {
		response = append(response, v)
	}

	return response
}

// Select implements outbound.HandlerSelector.
func (m *Manager) Select(selectors []string) []string {
	m.access.RLock()
	defer m.access.RUnlock()

	key := strings.Join(selectors, ",")
	if cache, ok := m.tagsCache.Load(key); ok {
		return cache.([]string)
	}

	tags := make([]string, 0, len(selectors))

	for tag := range m.taggedHandler {
		for _, selector := range selectors {
			if strings.HasPrefix(tag, selector) {
				tags = append(tags, tag)
				break
			}
		}
	}

	sort.Strings(tags)
	m.tagsCache.Store(key, tags)

	return tags
}

func init() {
	common.Must(common.RegisterConfig((*proxyman.OutboundConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return New(ctx, config.(*proxyman.OutboundConfig))
	}))
	common.Must(common.RegisterConfig((*core.OutboundHandlerConfig)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		return NewHandler(ctx, config.(*core.OutboundHandlerConfig))
	}))
}
