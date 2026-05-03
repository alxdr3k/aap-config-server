package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPrometheusIncludesCountersHistogramsAndGauges(t *testing.T) {
	ResetForTest()

	RecordHTTPRequest("GET", "/healthz", 200, 15*time.Millisecond)
	RecordReload("background", "updated", 50*time.Millisecond)
	RecordGitOperation("pull", "success", 20*time.Millisecond)
	RecordWatchWait("config", "timeout", 30*time.Second)

	body := string(RenderPrometheus([]GaugeSample{
		{
			Name: DegradedStateMetric,
			Labels: []Label{
				{Name: "component", Value: "store"},
			},
			Value: 1,
		},
	}))

	checks := []string{
		`# TYPE aap_config_server_http_requests_total counter`,
		`aap_config_server_http_requests_total{code="200",method="GET",route="/healthz"} 1`,
		`aap_config_server_http_request_duration_seconds_bucket{code="200",le="0.025",method="GET",route="/healthz"} 1`,
		`aap_config_server_reload_attempts_total{mode="background",outcome="updated"} 1`,
		`aap_config_server_git_operations_total{operation="pull",outcome="success"} 1`,
		`aap_config_server_watch_waits_total{outcome="timeout",resource="config"} 1`,
		`aap_config_server_degraded_state{component="store"} 1`,
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("metrics body missing %q:\n%s", check, body)
		}
	}
}

func TestEscapeLabelValues(t *testing.T) {
	ResetForTest()

	RecordHTTPRequest("GET", "/quote\"slash\\newline\n", 500, time.Millisecond)

	body := string(RenderPrometheus(nil))
	want := `route="/quote\"slash\\newline\n"`
	if !strings.Contains(body, want) {
		t.Fatalf("metrics body missing escaped label %q:\n%s", want, body)
	}
}
