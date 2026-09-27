package anytls

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

const paddingCheckMark = -1

const (
	maxPaddingSchemeBytes  = 8192
	maxPaddingRecords      = 256
	maxPaddingRanges       = 16
	maxPaddingSize         = 65535
	maxPaddingPacketBytes  = 128 * 1024
	maxPaddingSchemeBudget = 1024 * 1024
)

var DefaultPaddingScheme = []byte(`stop=8
0=30-30
1=100-400
2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000
3=9-9,500-1000
4=500-1000
5=500-1000
6=500-1000
7=500-1000`)

type paddingRange struct {
	minSize int
	maxSize int
}

type paddingFactory struct {
	rawScheme []byte
	md5Sum    string
	stop      uint32
	records   map[uint32][]paddingRange
}

func newPaddingFactory(rawScheme []byte) (*paddingFactory, error) {
	if len(rawScheme) == 0 || len(rawScheme) > maxPaddingSchemeBytes {
		return nil, ErrPaddingScheme
	}
	scheme := make(settings)
	for line := range strings.SplitSeq(string(rawScheme), "\n") {
		key, value, found := strings.Cut(line, "=")
		if _, duplicate := scheme[key]; !found || duplicate {
			return nil, ErrPaddingScheme
		}
		scheme[key] = value
	}
	stop, err := strconv.ParseUint(scheme["stop"], 10, 32)
	if err != nil || stop < 1 || stop > maxPaddingRecords {
		return nil, ErrPaddingScheme
	}
	sum := md5.Sum(rawScheme)
	factory := &paddingFactory{
		rawScheme: append([]byte(nil), rawScheme...),
		md5Sum:    hex.EncodeToString(sum[:]),
		stop:      uint32(stop),
		records:   make(map[uint32][]paddingRange),
	}
	var totalBudget int
	for key, value := range scheme {
		if key == "stop" {
			continue
		}
		packet, parseErr := strconv.ParseUint(key, 10, 32)
		if parseErr != nil || packet >= maxPaddingRecords || strconv.FormatUint(packet, 10) != key {
			return nil, ErrPaddingScheme
		}
		if strings.Count(value, ",") >= maxPaddingRanges {
			return nil, ErrPaddingScheme
		}
		var ranges []paddingRange
		var packetBudget int
		for item := range strings.SplitSeq(value, ",") {
			if item == "c" {
				ranges = append(ranges, paddingRange{minSize: paddingCheckMark})
				continue
			}
			minText, maxText, found := strings.Cut(item, "-")
			if !found {
				return nil, ErrPaddingScheme
			}
			minSize, minErr := strconv.Atoi(minText)
			if minErr != nil {
				return nil, ErrPaddingScheme
			}
			maxSize, maxErr := strconv.Atoi(maxText)
			if maxErr != nil {
				return nil, ErrPaddingScheme
			}
			if minSize <= 0 || maxSize < minSize || maxSize > maxPaddingSize {
				return nil, ErrPaddingScheme
			}
			packetBudget += maxSize
			totalBudget += maxSize
			if packetBudget > maxPaddingPacketBytes || totalBudget > maxPaddingSchemeBudget {
				return nil, ErrPaddingScheme
			}
			ranges = append(ranges, paddingRange{minSize: minSize, maxSize: maxSize})
		}
		if len(ranges) > 0 {
			factory.records[uint32(packet)] = ranges
		}
	}
	return factory, nil
}

// ValidatePaddingScheme is shared by configuration and remote control frames.
func ValidatePaddingScheme(raw []byte) error {
	_, err := newPaddingFactory(raw)
	return err
}

func paddingRecordSize(size int) (int, error) {
	// Check before arithmetic, even when called with a corrupted internal factory.
	if size < 1 || size > maxPaddingSize {
		return 0, E.New("anytls: padding size out of bounds")
	}
	wasteCount := 1 + (size-1)/maxFrameSize
	return size + wasteCount*frameOverhead, nil
}

func (f *paddingFactory) GenerateRecordPayloadSizes(packet uint32) []int {
	ranges := f.records[packet]
	if len(ranges) == 0 {
		return nil
	}
	sizes := make([]int, 0, len(ranges))
	for _, item := range ranges {
		switch {
		case item.minSize == paddingCheckMark:
			sizes = append(sizes, paddingCheckMark)
		case item.maxSize > item.minSize:
			offset := common.Must1(rand.Int(rand.Reader, big.NewInt(int64(item.maxSize-item.minSize))))
			sizes = append(sizes, item.minSize+int(offset.Int64()))
		default:
			sizes = append(sizes, item.minSize)
		}
	}
	return sizes
}

func (f *paddingFactory) GenerateHandshakePaddingSize() int {
	sizes := f.GenerateRecordPayloadSizes(0)
	if len(sizes) == 0 || sizes[0] < 0 {
		return 0
	}
	return sizes[0]
}
