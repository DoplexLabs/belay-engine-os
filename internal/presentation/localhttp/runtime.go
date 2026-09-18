package localhttp

import (
	"errors"
	"net/http"
)

const RuntimeSchemaVersion = "belay.local-runtime.v1"

type Experience string

const (
	ExperienceCurrent    Experience = "current"
	ExperienceValueFirst Experience = "value-first"
)

var ErrInvalidExperience = errors.New("invalid Local experience")

func ParseExperience(value string) (Experience, error) {
	experience := Experience(value)
	if !experience.Valid() {
		return "", ErrInvalidExperience
	}
	return experience, nil
}

func (e Experience) Valid() bool {
	return e == ExperienceCurrent || e == ExperienceValueFirst
}

type runtimeResponse struct {
	SchemaVersion string     `json:"schema_version"`
	Experience    Experience `json:"experience"`
}

func (s *Server) getRuntime(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	writeJSON(w, http.StatusOK, runtimeResponse{
		SchemaVersion: RuntimeSchemaVersion,
		Experience:    s.experience,
	})
}
