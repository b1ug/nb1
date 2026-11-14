package control

// TransportMetric is an optional driver's cumulative transport-counter
// snapshot. Calls counts SDK operations, Attempts includes the initial call,
// Retries counts additional attempts, and Failures counts terminally failed
// operations. LastError is historical: the operation may since have recovered.
// Drivers expose Metrics() []TransportMetric without changing Backend/Service.
type TransportMetric struct {
	Serial    string `json:"serial"`
	Operation string `json:"operation"`
	Calls     uint64 `json:"calls"`
	Attempts  uint64 `json:"attempts"`
	Retries   uint64 `json:"retries"`
	Failures  uint64 `json:"failures"`
	LastError string `json:"last_error,omitempty"`
}

func (e *Engine) TransportMetrics() []TransportMetric {
	if provider, ok := e.driver.(interface{ Metrics() []TransportMetric }); ok {
		if metrics := provider.Metrics(); metrics != nil {
			return metrics
		}
	}
	return []TransportMetric{}
}
