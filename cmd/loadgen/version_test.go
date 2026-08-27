package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionFrom(t *testing.T) {
	setting := func(k, v string) debug.BuildSetting { return debug.BuildSetting{Key: k, Value: v} }

	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{
			// Локальная сборка: Go кладёт в Main.Version псевдоверсию, но рядом
			// лежит настоящий хеш — берём его, иначе -version печатает 45 символов.
			name: "рабочая копия с правками",
			info: debug.BuildInfo{
				Main: debug.Module{Version: "v0.0.0-20260827120054-5f62ef91018b+dirty"},
				Settings: []debug.BuildSetting{
					setting("vcs.revision", "5f62ef91018b46e7cd73b35f0699107567905e5b"),
					setting("vcs.modified", "true"),
				},
			},
			want: "5f62ef9-dirty",
		},
		{
			name: "чистая рабочая копия",
			info: debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					setting("vcs.revision", "5f62ef91018b46e7cd73b35f0699107567905e5b"),
					setting("vcs.modified", "false"),
				},
			},
			want: "5f62ef9",
		},
		{
			name: "go install pkg@v1.2.3 — VCS-данных нет",
			info: debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}},
			want: "v1.2.3",
		},
		{
			name: "ни версии, ни VCS",
			info: debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			want: "dev",
		},
		{
			name: "короткий хеш не обрезаем",
			info: debug.BuildInfo{
				Settings: []debug.BuildSetting{setting("vcs.revision", "abc")},
			},
			want: "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionFrom(&tt.info); got != tt.want {
				t.Errorf("versionFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}
