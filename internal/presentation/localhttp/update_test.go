package localhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/updatecheck"
)

type updateServiceStub struct {
	status updatecheck.Status
	action string
}

func (service *updateServiceStub) Status() updatecheck.Status {
	return service.status
}

func (service *updateServiceStub) Remind(version string) error {
	service.action = "remind:" + version
	service.status.UpdateAvailable = false
	return nil
}

func (service *updateServiceStub) Dismiss(version string) error {
	service.action = "dismiss:" + version
	service.status.UpdateAvailable = false
	return nil
}

func TestUpdateRoutesAreAuthenticatedAndBounded(t *testing.T) {
	updates := &updateServiceStub{status: updatecheck.Status{
		SchemaVersion:   updatecheck.SchemaVersion,
		Enabled:         true,
		CurrentVersion:  "0.0.1-alpha.5",
		LatestVersion:   "0.0.1-alpha.6",
		UpdateAvailable: true,
	}}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithUpdateService(updates),
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/update", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/v1/update",
		strings.NewReader(`{"action":"remind","version":"0.0.1-alpha.6"}`),
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("remind status = %d body=%s", response.Code, response.Body.String())
	}
	if updates.action != "remind:0.0.1-alpha.6" {
		t.Fatalf("action = %q", updates.action)
	}
}
