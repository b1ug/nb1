// Package nativeinspection collects explicit native read observations without
// playback, storage, retries, device discovery or handle ownership of its own.
package nativeinspection

import (
	"errors"
	"time"

	"github.com/b1ug/nb1/internal/devicesequence"
)

const (
	Contract = "nb1-native-inspection/experimental-v1"
	RAM      = "ram"
	Playback = "playback"
)

var (
	ErrInvalid     = errors.New("invalid native inspection request")
	ErrUnsupported = errors.New("native inspection unavailable")
)

type Request struct {
	Kind  string
	Range *devicesequence.Range
}

// Validate never queries a device. RAM ranges are explicit, without sentinels.
func (r Request) Validate() error {
	switch r.Kind {
	case RAM:
		if r.Range == nil || r.Range.StartLine < 0 || r.Range.StartLine >= 32 || r.Range.LineCount < 1 || r.Range.LineCount > 32-r.Range.StartLine {
			return ErrInvalid
		}
	case Playback:
		if r.Range != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (r Request) Clone() Request {
	if r.Range != nil {
		value := *r.Range
		r.Range = &value
	}
	return r
}

type Identity struct {
	Serial         string `json:"serial"`
	Product        string `json:"product"`
	Firmware       int    `json:"firmware"`
	FirmwareSource string `json:"firmware_source"`
}

type ErrorInfo struct {
	Code                       string  `json:"code"`
	Message                    string  `json:"message"`
	MessageTruncated           bool    `json:"message_truncated"`
	MessageUTF8Replaced        bool    `json:"message_utf8_replaced"`
	NativePlatform             *string `json:"native_platform"`
	NativeCode                 *string `json:"native_code"`
	NativePlatformTruncated    bool    `json:"native_platform_truncated"`
	NativePlatformUTF8Replaced bool    `json:"native_platform_utf8_replaced"`
}

// Report exports bytes as hexadecimal, including only the returned prefix.
// This is the SDK/GID buffer boundary, not a physical USB transaction capture.
type Report struct {
	Layout            string `json:"layout"`
	RequestedReportID byte   `json:"requested_report_id"`
	BufferLength      int    `json:"buffer_length"`
	ReadAttempted     bool   `json:"read_attempted"`
	ReturnedLength    int    `json:"returned_length"`
	LengthValid       bool   `json:"length_valid"`
	PrefixTrusted     bool   `json:"prefix_trusted"`
	RawHex            string `json:"raw_hex"`
	UnknownTailHex    string `json:"unknown_tail_hex"`
}

// Reply is an adapter's in-memory observation, never default-marshaled to wire.
type Reply struct {
	RequestedReportID byte
	BufferLength      int
	ReadAttempted     bool
	ReturnedLength    int
	LengthValid       bool
	Bytes             []byte
}

type LineRead struct {
	Position      int
	RGB8          *[3]byte
	DurationTicks *uint16
	LEDCode       *byte
	Reply         Reply
}

type PlaybackRead struct {
	Fields devicesequence.PlaybackFields
	Reply  Reply
}

// Reader is called synchronously by the owning worker only. Cancellation cannot
// forcibly interrupt an in-flight native operation or close its handle.
type Reader interface {
	ReadLine(position int) (LineRead, error)
	ReadPlayback() (PlaybackRead, error)
}

type ReadReference struct {
	Firmware            int      `json:"firmware"`
	Applicability       string   `json:"applicability"`
	Sources             []string `json:"sources"`
	RowsDecodable       bool     `json:"rows_decodable"`
	PlaybackEndEncoding string   `json:"playback_end_encoding"`
}

// Profile is trusted adapter input; HTTP/CLI/MCP callers cannot supply it.
// SDK reference facts remain distinct from version-specific read interpretation.
type Profile struct {
	SDKModel               string                         `json:"sdk_model"`
	SDKReferenceFirmware   *int                           `json:"sdk_reference_firmware"`
	SDKApplicability       string                         `json:"sdk_applicability"`
	RAMLines               *int                           `json:"ram_lines"`
	ReadReference          ReadReference                  `json:"read_reference"`
	OperationQualification string                         `json:"operation_qualification"`
	PlaybackProfile        devicesequence.PlaybackProfile `json:"-"`
}

type Row struct {
	Position      int        `json:"position"`
	RGB8          *[3]byte   `json:"rgb8"`
	DurationTicks *uint16    `json:"duration_ticks"`
	LEDCode       *byte      `json:"led_code"`
	Report        Report     `json:"report"`
	Error         *ErrorInfo `json:"error"`
}

type RAMResult struct {
	RequestedRange  devicesequence.Range     `json:"requested_range"`
	Rows            []Row                    `json:"rows"`
	UnreadPositions []int                    `json:"unread_positions"`
	Sequence        *devicesequence.Sequence `json:"sequence"`
	SequenceDigest  *string                  `json:"sequence_digest"`
	DecodingStatus  string                   `json:"decoding_status"`
	Issues          []devicesequence.Issue   `json:"issues"`
	Deployable      bool                     `json:"deployable"`
}

type PlaybackResult struct {
	Fields          devicesequence.PlaybackFields `json:"raw_fields"`
	Report          Report                        `json:"report"`
	ConfiguredRange *devicesequence.Range         `json:"configured_range"`
	DecodingStatus  string                        `json:"decoding_status"`
	ProfileDigest   string                        `json:"profile_digest"`
	Issues          []devicesequence.Issue        `json:"issues"`
	Deployable      bool                          `json:"deployable"`
	Error           *ErrorInfo                    `json:"error"`
}

type Result struct {
	Contract         string          `json:"contract"`
	Kind             string          `json:"kind"`
	DaemonInstanceID string          `json:"daemon_instance_id"`
	DeviceSessionID  string          `json:"device_session_id"`
	Device           Identity        `json:"device"`
	Source           string          `json:"source"`
	Consistency      string          `json:"consistency"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       time.Time       `json:"finished_at"`
	DurationNS       int64           `json:"duration_ns"`
	Status           string          `json:"status"`
	Complete         bool            `json:"complete"`
	Profile          Profile         `json:"profile"`
	RAM              *RAMResult      `json:"ram"`
	Playback         *PlaybackResult `json:"playback"`
	Error            *ErrorInfo      `json:"error"`
}
