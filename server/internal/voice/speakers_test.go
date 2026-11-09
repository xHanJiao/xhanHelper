package voice

import "testing"

func TestResolveSpeaker(t *testing.T) {
	const def = "zh_female_vv_uranus_bigtts"
	const curated = "zh_male_m191_uranus_bigtts"

	cases := []struct {
		name      string
		requested string
		want      string
		fallback  bool
	}{
		{"empty uses default", "", def, false},
		{"curated accepted", curated, curated, false},
		{"unknown falls back", "not_a_voice", def, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, fallback := ResolveSpeaker(def, c.requested)
			if got != c.want || fallback != c.fallback {
				t.Fatalf("ResolveSpeaker(%q, %q) = (%q, %v), want (%q, %v)",
					def, c.requested, got, fallback, c.want, c.fallback)
			}
		})
	}
}

func TestResolveSpeakerAcceptsNonCuratedDefault(t *testing.T) {
	const clone = "clone_voice_1"
	got, fallback := ResolveSpeaker(clone, clone)
	if got != clone || fallback {
		t.Fatalf("ResolveSpeaker(%q, %q) = (%q, %v)", clone, clone, got, fallback)
	}
}

func TestSpeakerListPrependsNonCuratedDefault(t *testing.T) {
	list := SpeakerList("clone_voice_1")
	if len(list) != len(curatedSpeakers)+1 {
		t.Fatalf("len = %d, want %d", len(list), len(curatedSpeakers)+1)
	}
	if list[0].ID != "clone_voice_1" || list[0].Name != "默认（配置）" {
		t.Fatalf("first = %+v", list[0])
	}
}

func TestSpeakerListKeepsCuratedDefault(t *testing.T) {
	const def = "zh_female_vv_uranus_bigtts"
	list := SpeakerList(def)
	if len(list) != len(curatedSpeakers) {
		t.Fatalf("len = %d, want %d", len(list), len(curatedSpeakers))
	}
	if list[0].ID != def {
		t.Fatalf("first = %+v", list[0])
	}
}
