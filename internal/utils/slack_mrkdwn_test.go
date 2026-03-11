package utils

import "testing"

func TestStripSlackMrkdwn(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "plain text unchanged",
			input: "High CPU on web-01",
			want:  "High CPU on web-01",
		},
		{
			name:  "link with display text",
			input: `<https://example.com|Click here>`,
			want:  "Click here",
		},
		{
			name:  "bare link",
			input: `<https://example.com/path?q=1>`,
			want:  "https://example.com/path?q=1",
		},
		{
			name:  "user mention removed",
			input: `Hello <@U012ABC34> please check`,
			want:  "Hello please check",
		},
		{
			name:  "channel mention with name",
			input: `See <#C012ABC34|general> for details`,
			want:  "See #general for details",
		},
		{
			name:  "channel mention without name",
			input: `See <#C012ABC34> for details`,
			want:  "See for details",
		},
		{
			name:  "emoji codes removed",
			input: `:red_circle: :warning: alert fired`,
			want:  "alert fired",
		},
		{
			name:  "bold stripped",
			input: `This is *bold text* here`,
			want:  "This is bold text here",
		},
		{
			name:  "strikethrough stripped",
			input: `This is ~struck text~ here`,
			want:  "This is struck text here",
		},
		{
			name:  "inline code stripped",
			input: "Run `kubectl get pods` now",
			want:  "Run kubectl get pods now",
		},
		{
			name:  "HTML entities decoded",
			input: `a &amp; b &lt; c &gt; d`,
			want:  "a & b < c > d",
		},
		{
			name:  "underscores in hostnames preserved",
			input: `Alert on web_server_01 and db_master_02`,
			want:  "Alert on web_server_01 and db_master_02",
		},
		{
			name:  "real PagerDuty example",
			input: `:red_circle: *<https://gcorelabs.pagerduty.com/incidents/Q31UTHHOLNK1OU?utm_campaign=channel&amp;utm_source=ij0KKKK|[#12345] High CPU on web-01>* triggered`,
			want:  "[#12345] High CPU on web-01 triggered",
		},
		{
			name:  "combined formatting",
			input: `:warning: *<https://example.com|Alert>*: Host <@U012ABC34> reported ~old issue~ in <#C012ABC34|ops>`,
			want:  "Alert: Host reported old issue in #ops",
		},
		{
			name:  "multiple spaces collapsed",
			input: `hello   world    test`,
			want:  "hello world test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripSlackMrkdwn(tt.input)
			if got != tt.want {
				t.Errorf("StripSlackMrkdwn() = %q, want %q", got, tt.want)
			}
		})
	}
}

func BenchmarkStripSlackMrkdwn(b *testing.B) {
	input := `:red_circle: *<https://gcorelabs.pagerduty.com/incidents/Q31UTHHOLNK1OU?utm_campaign=channel&amp;utm_source=ij0KKKK|[#12345] High CPU on web-01>* triggered in <#C012ABC34|ops-alerts>`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StripSlackMrkdwn(input)
	}
}
