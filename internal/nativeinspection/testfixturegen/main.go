// Command testfixturegen emits synthetic protocol vectors. It never enumerates,
// opens or communicates with hardware and does not represent physical evidence.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/b1ug/nb1/internal/devicesequence"
	"github.com/b1ug/nb1/internal/nativeinspection"
)

type fixtureReader struct{}

func (fixtureReader) ReadLine(position int) (nativeinspection.LineRead, error) {
	data := []byte{1, 'R', 255, 0, 0, 0, 50, 0, 0xfe}
	if position == 1 {
		data = []byte{1, 'R', 0, 0, 255, 0, 50, 2, 0xfd}
	}
	return nativeinspection.LineRead{Position: position, Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: data}}, nil
}

type fixtureError struct{}

func (fixtureError) Error() string          { return "Synthetic query error; not hardware evidence" }
func (fixtureError) NativePlatform() string { return "fixture" }
func (fixtureError) NativeCode() uint64     { return ^uint64(0) }
func (fixtureReader) ReadPlayback() (nativeinspection.PlaybackRead, error) {
	return nativeinspection.PlaybackRead{Reply: nativeinspection.Reply{RequestedReportID: 1, BufferLength: 9, ReadAttempted: true, ReturnedLength: 9, LengthValid: true, Bytes: []byte{1, 'S', 1, 0, 2, 0, 1, 0xaa, 0xbb}}}, fixtureError{}
}

func main() {
	directory := flag.String("out", "", "new local directory for synthetic JSON vectors")
	flag.Parse()
	if *directory == "" {
		panic("--out is required")
	}
	if err := os.MkdirAll(*directory, 0o700); err != nil {
		panic(err)
	}
	ram, ref := 32, 205
	profile := nativeinspection.Profile{SDKModel: "mk2", SDKReferenceFirmware: &ref, SDKApplicability: "model-only", RAMLines: &ram, OperationQualification: "unverified", ReadReference: nativeinspection.ReadReference{Firmware: 204, Applicability: "firmware-matched", Sources: []string{"fixture://not-hardware"}, RowsDecodable: true, PlaybackEndEncoding: "exclusive"}, PlaybackProfile: devicesequence.PlaybackProfile{Capabilities: devicesequence.Capabilities{Model: "mk2", Firmware: 204, ReferenceFirmware: 204, Applicability: "firmware-matched", RAMLines: &ram, Sources: []string{"fixture://not-hardware"}}, EndEncoding: "exclusive"}}
	vectors := []struct {
		name    string
		request nativeinspection.Request
	}{{"ram-complete.json", nativeinspection.Request{Kind: nativeinspection.RAM, Range: &devicesequence.Range{StartLine: 0, LineCount: 2}}}, {"playback-partial.json", nativeinspection.Request{Kind: nativeinspection.Playback}}}
	manifest := map[string]any{"kind": "synthetic-native-inspection-vectors", "hardware_evidence": false, "device_io": false, "files": map[string]any{}}
	for _, vector := range vectors {
		result, err := nativeinspection.Collect(context.Background(), fixtureReader{}, vector.request, nativeinspection.Identity{Serial: "20000001", Product: "Synthetic blink(1) MK2 fixture", Firmware: 204, FirmwareSource: "cached-on-open"}, profile)
		if vector.request.Kind == nativeinspection.RAM && err != nil {
			panic(err)
		}
		if vector.request.Kind == nativeinspection.Playback {
			var marker fixtureError
			if !errors.As(err, &marker) {
				panic("partial fixture lost its native error")
			}
		}
		result.DaemonInstanceID, result.DeviceSessionID = "daemon_fixture", "session_0000000000000001"
		result.StartedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		result.FinishedAt = result.StartedAt.Add(time.Millisecond)
		result.DurationNS = int64(time.Millisecond)
		data, err := nativeinspection.MarshalResult(result, "20000001", vector.request)
		if err != nil {
			panic(err)
		}
		data = append(data, '\n')
		file, err := os.OpenFile(filepath.Join(*directory, vector.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			panic(err)
		}
		if _, err = file.Write(data); err != nil {
			panic(err)
		}
		if err = file.Close(); err != nil {
			panic(err)
		}
		digest := sha256.Sum256(data)
		manifest["files"].(map[string]any)[vector.name] = map[string]any{"sha256": hex.EncodeToString(digest[:]), "bytes": len(data)}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	file, err := os.OpenFile(filepath.Join(*directory, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		panic(err)
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
	fmt.Println("Synthetic native inspection vectors written; no device I/O")
}
