package patterns

import (
	"strconv"

	"github.com/b1ug/nb1/internal/control"
)

// canonicalWriter is intentionally separate from encoding/json: the record
// domain requires C0 as lower \u00xx, direct HTML/U+2028/U+2029, and fixed keys.
// Every append is bounded before allocation; callers receive no partial bytes.
type canonicalWriter struct {
	data []byte
	err  error
}

func (w *canonicalWriter) raw(value string) {
	if w.err != nil {
		return
	}
	if len(value) > MaxCanonicalBytes-len(w.data) {
		w.err = invalid("canonical snapshot exceeds 1 MiB")
		return
	}
	w.data = append(w.data, value...)
}

func (w *canonicalWriter) text(value string) {
	const digits = "0123456789abcdef"
	w.raw(`"`)
	start := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c != '"' && c != '\\' && c >= 0x20 {
			continue
		}
		w.raw(value[start:i])
		switch c {
		case '"':
			w.raw(`\"`)
		case '\\':
			w.raw(`\\`)
		default:
			w.raw(string([]byte{'\\', 'u', '0', '0', digits[c>>4], digits[c&15]}))
		}
		start = i + 1
	}
	w.raw(value[start:])
	w.raw(`"`)
}

func (w *canonicalWriter) integer(value int) { w.raw(strconv.Itoa(value)) }

func (w *canonicalWriter) program(value control.Program) {
	w.raw(`{"steps":[`)
	for i, step := range value.Steps {
		if i > 0 {
			w.raw(",")
		}
		w.raw(`{"led":`)
		w.integer(step.LED)
		w.raw(`,"color":`)
		w.text(step.Color)
		w.raw(`,"fade_ms":`)
		w.integer(step.FadeMS)
		w.raw(`,"hold_ms":`)
		w.integer(step.HoldMS)
		w.raw("}")
	}
	w.raw(`],"repeat":`)
	w.integer(value.Repeat)
	w.raw(`,"on_finish":`)
	w.text(value.OnFinish)
	w.raw("}")
}

func (w *canonicalWriter) source(value Source) {
	if value.Program != nil {
		w.raw(`{"program":`)
		w.program(*value.Program)
		w.raw("}")
		return
	}
	effect := value.Effect
	w.raw(`{"effect":{"kind":`)
	w.text(effect.Kind)
	w.raw(`,"color":`)
	w.text(effect.Color)
	w.raw(`,"led":`)
	w.integer(effect.LED)
	if effect.Kind == "steady" {
		w.raw(`,"fade_ms":`)
		w.integer(*effect.FadeMS)
	} else {
		w.raw(`,"cycles":`)
		w.integer(*effect.Cycles)
		w.raw(`,"period_ms":`)
		w.integer(*effect.PeriodMS)
		w.raw(`,"on_finish":`)
		w.text(*effect.OnFinish)
	}
	w.raw("}}")
}

func (w *canonicalWriter) record(value Record, revision bool) {
	w.raw(`{"id":`)
	w.text(value.ID)
	w.raw(`,"title":`)
	w.text(value.Title)
	w.raw(`,"source":`)
	w.source(value.Source)
	w.raw(`,"compiled_program":`)
	w.program(value.CompiledProgram)
	w.raw(`,"compile_contract":`)
	w.text(value.CompileContract)
	if revision {
		w.raw(`,"revision":`)
		w.text(value.Revision)
	}
	w.raw("}")
}

func encodeRecordJSON(record Record) ([]byte, error) {
	var writer canonicalWriter
	writer.record(record, false)
	if writer.err != nil {
		return nil, writer.err
	}
	return writer.data, nil
}

func encodeSnapshot(records []Record) ([]byte, error) {
	var writer canonicalWriter
	writer.raw(`{"schema_version":1,"kind":"nb1-pattern-snapshot","records":[`)
	for i, record := range records {
		if i > 0 {
			writer.raw(",")
		}
		writer.record(record, true)
	}
	writer.raw("]}")
	if writer.err != nil {
		return nil, writer.err
	}
	return writer.data, nil
}
