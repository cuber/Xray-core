package stats

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	feature_stats "github.com/xtls/xray-core/features/stats"
)

type DomainTrafficEntry = feature_stats.DomainTrafficEntry
type DomainTrafficBucket = feature_stats.DomainTrafficBucket
type DomainTrafficSnapshot = feature_stats.DomainTrafficSnapshot

const (
	defaultDomainTrafficInterval   = 5 * time.Second
	defaultDomainTrafficRetention  = 5 * time.Minute
	defaultDomainTrafficMaxDomains = 512
)

type domainTrafficBucket struct {
	start    time.Time
	sequence uint64
	entries  map[string]*DomainTrafficEntry
	other    DomainTrafficEntry
	unknown  DomainTrafficEntry
}

type domainTraffic struct {
	mu         sync.Mutex
	bootID     string
	interval   time.Duration
	retention  time.Duration
	maxDomains int
	clock      func() time.Time
	buckets    map[int64]*domainTrafficBucket
	lastSeen   map[string]time.Time
	nextSeq    uint64
}

func newDomainTraffic(config *DomainTrafficConfig) *domainTraffic {
	if config == nil || !config.Enabled {
		return nil
	}
	interval := time.Duration(config.BucketIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultDomainTrafficInterval
	}
	retention := time.Duration(config.RetentionSeconds) * time.Second
	if retention <= 0 {
		retention = defaultDomainTrafficRetention
	}
	maxDomains := int(config.MaxDomains)
	if maxDomains <= 0 {
		maxDomains = defaultDomainTrafficMaxDomains
	}
	return &domainTraffic{
		bootID:     newBootID(),
		interval:   interval,
		retention:  retention,
		maxDomains: maxDomains,
		clock:      time.Now,
		buckets:    make(map[int64]*domainTrafficBucket),
		lastSeen:   make(map[string]time.Time),
		nextSeq:    1,
	}
}

func newBootID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
}

func normalizeDomainTrafficDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return "unknown"
	}
	return domain
}

func (d *domainTraffic) nowBucket() (time.Time, int64) {
	start := d.clock().Truncate(d.interval)
	return start, start.UnixNano()
}

func (d *domainTraffic) pruneLocked(now time.Time) {
	cutoff := now.Add(-d.retention).Truncate(d.interval).UnixNano()
	for key := range d.buckets {
		if key < cutoff {
			delete(d.buckets, key)
		}
	}
	for domain := range d.lastSeen {
		if !d.domainExistsLocked(domain) {
			delete(d.lastSeen, domain)
		}
	}
}

func (d *domainTraffic) Record(domain string, uplinkBytes, downlinkBytes uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	start, key := d.nowBucket()
	d.pruneLocked(start)
	bucket := d.buckets[key]
	if bucket == nil {
		bucket = &domainTrafficBucket{start: start, sequence: d.nextSeq, entries: make(map[string]*DomainTrafficEntry)}
		d.nextSeq++
		d.buckets[key] = bucket
	}
	domain = normalizeDomainTrafficDomain(domain)
	if domain == "unknown" {
		bucket.unknown.UplinkBytes = saturatingAdd(bucket.unknown.UplinkBytes, uplinkBytes)
		bucket.unknown.DownlinkBytes = saturatingAdd(bucket.unknown.DownlinkBytes, downlinkBytes)
		return
	}
	entry, ok := bucket.entries[domain]
	if !ok {
		if !d.ensureDomainLocked(domain) {
			bucket.other.UplinkBytes = saturatingAdd(bucket.other.UplinkBytes, uplinkBytes)
			bucket.other.DownlinkBytes = saturatingAdd(bucket.other.DownlinkBytes, downlinkBytes)
			return
		}
		entry = &DomainTrafficEntry{Domain: domain}
		bucket.entries[domain] = entry
	}
	d.lastSeen[domain] = d.clock()
	entry.UplinkBytes = saturatingAdd(entry.UplinkBytes, uplinkBytes)
	entry.DownlinkBytes = saturatingAdd(entry.DownlinkBytes, downlinkBytes)
}

func (d *domainTraffic) ensureDomainLocked(domain string) bool {
	if d.domainExistsLocked(domain) {
		return true
	}
	if len(d.domainsLocked()) < d.maxDomains {
		return true
	}
	oldest := ""
	var oldestSeen time.Time
	for _, candidate := range d.domainsLocked() {
		lastSeen := d.lastSeen[candidate]
		if oldest == "" || lastSeen.Before(oldestSeen) {
			oldest, oldestSeen = candidate, lastSeen
		}
	}
	if oldest == "" {
		return false
	}
	for _, bucket := range d.buckets {
		if entry, ok := bucket.entries[oldest]; ok {
			bucket.other.UplinkBytes = saturatingAdd(bucket.other.UplinkBytes, entry.UplinkBytes)
			bucket.other.DownlinkBytes = saturatingAdd(bucket.other.DownlinkBytes, entry.DownlinkBytes)
			delete(bucket.entries, oldest)
		}
	}
	delete(d.lastSeen, oldest)
	return true
}

func (d *domainTraffic) domainExistsLocked(domain string) bool {
	for _, bucket := range d.buckets {
		if _, ok := bucket.entries[domain]; ok {
			return true
		}
	}
	return false
}

func (d *domainTraffic) domainsLocked() []string {
	seen := make(map[string]struct{})
	for _, bucket := range d.buckets {
		for domain := range bucket.entries {
			seen[domain] = struct{}{}
		}
	}
	domains := make([]string, 0, len(seen))
	for domain := range seen {
		domains = append(domains, domain)
	}
	return domains
}

func (d *domainTraffic) Snapshot(afterBootID string, afterSequence uint64, maxBuckets uint32) DomainTrafficSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.clock()
	d.pruneLocked(now)
	response := DomainTrafficSnapshot{Enabled: true, BootID: d.bootID}
	keys := make([]int64, 0, len(d.buckets))
	for key := range d.buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if len(keys) == 0 {
		return response
	}
	response.OldestSequence = d.buckets[keys[0]].sequence
	response.LatestSequence = d.buckets[keys[len(keys)-1]].sequence
	if afterBootID == d.bootID && response.OldestSequence > afterSequence+1 {
		response.HasGap = true
	}
	for _, key := range keys {
		sequence := d.buckets[key].sequence
		if afterBootID == d.bootID && sequence <= afterSequence {
			continue
		}
		bucket := d.buckets[key]
		if bucket.start.Add(d.interval).After(now) {
			continue
		}
		item := DomainTrafficBucket{
			BootID: d.bootID, Sequence: sequence,
			StartUnix: bucket.start.Unix(), EndUnix: bucket.start.Add(d.interval).Unix(),
			OtherUplinkBytes: bucket.other.UplinkBytes, OtherDownlinkBytes: bucket.other.DownlinkBytes,
			UnknownUplinkBytes: bucket.unknown.UplinkBytes, UnknownDownlinkBytes: bucket.unknown.DownlinkBytes,
		}
		for _, entry := range bucket.entries {
			item.Entries = append(item.Entries, *entry)
		}
		sort.Slice(item.Entries, func(i, j int) bool { return item.Entries[i].Domain < item.Entries[j].Domain })
		response.Buckets = append(response.Buckets, item)
		if maxBuckets > 0 && uint32(len(response.Buckets)) >= maxBuckets {
			break
		}
	}
	return response
}

func saturatingAdd(current, delta uint64) uint64 {
	if ^uint64(0)-current < delta {
		return ^uint64(0)
	}
	return current + delta
}
