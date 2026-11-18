package webui

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// The complete Nightlab profile remains closed. It is populated from the
// reviewed frontend asset contract, never from a manifest-supplied wildcard.
var nightlabAssetNames = append(append([]string(nil), assetNames...), []string{
	"nightlab/airwave-tokens.css", "nightlab/airwave.css",
	"nightlab/studio.css", "nightlab/embedded-studio.css", "nightlab/studio-skins.css",
	"nightlab/studio.js", "nightlab/composer-ui.js", "nightlab/studio-appearance.js",
	"nightlab/studio-preview.js", "nightlab/preview-timeline.js", "nightlab/compiler-loader.js",
	"nightlab/readback.js", "nightlab/time-format.js",
	"nightlab/studio-3d.js", "nightlab/light-model.js", "nightlab/enclosure-fit.js", "nightlab/THREE-LICENSE.txt",
	"nightlab/compiler/nb1.wasm", "nightlab/compiler/wasm_exec.js",
	"nightlab/compiler/compiler-source.json", "nightlab/compiler/GO-LICENSE.txt", "nightlab/compiler/NB1-LICENSE.txt", "nightlab/compiler/COLORKIT-LICENSE.txt",
	"nightlab/model-assets/blink1mk2_enclosure_diffuser_v3k.stl",
	"nightlab/model-assets/blink1mk2_enclosure_frame_v3k.stl",
	"nightlab/model-assets/source.json", "nightlab/model-assets/seating-fit.json",
	"nightlab/model-assets/LICENSE.txt",
	"nightlab/model.html", "nightlab/model-notes.js", "nightlab/home.css",
}...)

func nightlabBaseAsset(name string) bool {
	if !strings.HasPrefix(name, "nightlab/") {
		return true
	}
	for _, prefix := range []string{"nightlab/compiler/", "nightlab/model-assets/"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	switch name {
	case "nightlab/studio-3d.js", "nightlab/light-model.js", "nightlab/enclosure-fit.js", "nightlab/THREE-LICENSE.txt", "nightlab/model.html", "nightlab/model-notes.js", "nightlab/home.css":
		return false
	}
	return true
}

func decodeAsset(stored []byte, encoding string, decodedSize int64) ([]byte, error) {
	if decodedSize <= 0 || decodedSize > maxNightlabDecodedBytes {
		return nil, fmt.Errorf("invalid decoded asset size")
	}
	switch encoding {
	case "identity":
		if int64(len(stored)) != decodedSize {
			return nil, fmt.Errorf("identity asset size mismatch")
		}
		return stored, nil
	case "gzip":
		input := bytes.NewReader(stored)
		reader, err := gzip.NewReader(input)
		if err != nil {
			return nil, err
		}
		reader.Multistream(false)
		decoded, readErr := io.ReadAll(io.LimitReader(reader, decodedSize+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(decoded)) != decodedSize || input.Len() != 0 {
			return nil, fmt.Errorf("invalid, oversized or trailing compressed asset")
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported asset encoding")
	}
}
