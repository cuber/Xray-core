package command_test

import (
	"testing"

	stats "github.com/xtls/xray-core/app/stats"
	cmd "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestDomainTrafficWireFields(t *testing.T) {
	// Field order is the original wire numbering, not Go declaration order.
	for _, tc := range []struct {
		message proto.Message
		fields  []protoreflect.Name
	}{
		{&stats.Config{}, []protoreflect.Name{"domain_traffic"}},
		{&stats.DomainTrafficConfig{}, []protoreflect.Name{"enabled", "bucket_interval_seconds", "retention_seconds", "max_domains"}},
		{&cmd.GetDomainTrafficBucketsRequest{}, []protoreflect.Name{"after_boot_id", "after_sequence", "max_buckets"}},
		{&cmd.DomainTrafficEntry{}, []protoreflect.Name{"domain", "uplink_bytes", "downlink_bytes", "user"}},
		{&cmd.DomainTrafficBucket{}, []protoreflect.Name{"boot_id", "sequence", "start_unix", "end_unix", "entries", "other_uplink_bytes", "other_downlink_bytes", "unknown_uplink_bytes", "unknown_downlink_bytes"}},
		{&cmd.GetDomainTrafficBucketsResponse{}, []protoreflect.Name{"enabled", "boot_id", "oldest_sequence", "latest_sequence", "has_gap", "buckets"}},
	} {
		d := tc.message.ProtoReflect().Descriptor()
		for i, name := range tc.fields {
			field := d.Fields().ByName(name)
			if field == nil || field.Number() != protoreflect.FieldNumber(i+1) {
				t.Fatalf("%s.%s wire number changed", d.FullName(), name)
			}
		}
	}
	// Anonymous legacy entries remain anonymous; user was added as field 4.
	legacy := []byte{0x0a, 0x01, 'a', 0x10, 0x64, 0x18, 0xc8, 0x01}
	entry := new(cmd.DomainTrafficEntry)
	if err := proto.Unmarshal(legacy, entry); err != nil {
		t.Fatal(err)
	}
	if entry.Domain != "a" || entry.User != "" || entry.UplinkBytes != 100 || entry.DownlinkBytes != 200 {
		t.Fatalf("legacy entry: %v", entry)
	}
	withUser := append(append([]byte(nil), legacy...), 0x22, 0x05, 'a', 'l', 'i', 'c', 'e')
	if err := proto.Unmarshal(withUser, entry); err != nil {
		t.Fatal(err)
	}
	if entry.User != "alice" || entry.UplinkBytes != 100 || entry.DownlinkBytes != 200 {
		t.Fatalf("user entry: %v", entry)
	}
	service := cmd.File_app_stats_command_command_proto.Services().ByName("StatsService")
	for _, name := range []protoreflect.Name{"GetStats", "GetStatsOnline", "QueryStats", "GetSysStats", "GetStatsOnlineIpList", "GetAllOnlineUsers", "GetUsersStats", "GetDomainTrafficBuckets"} {
		if service.Methods().ByName(name) == nil {
			t.Fatalf("lost RPC %s", name)
		}
	}
}
