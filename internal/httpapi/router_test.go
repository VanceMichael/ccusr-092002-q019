package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	db := openTestDB(t)
	return NewAPI(domain.NewService(db)).Router()
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", response.Code)
	}
}
