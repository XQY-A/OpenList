package common

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/gin-gonic/gin"
)

func TestErrorMessageFiltersStructuredErrors(t *testing.T) {
	previous := conf.PrivacyReg
	conf.PrivacyReg = []*regexp.Regexp{regexp.MustCompile(`access_token=[^ &]+`)}
	t.Cleanup(func() { conf.PrivacyReg = previous })
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "nil"},
		{name: "plain", err: errors.New("cannot save task"), want: "cannot save task"},
		{
			name: "private", err: errors.New("request failed: access_token=secret&retry=0"),
			want: "request failed: " + strings.Repeat("*", len("access_token=secret")) + "&retry=0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ErrorMessage(test.err); got != test.want {
				t.Fatalf("ErrorMessage = %q, want %q", got, test.want)
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ErrorWithDataResp(ctx, test.err, 500, gin.H{"error": ErrorMessage(test.err)})
			var result Resp[map[string]string]
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != 500 || result.Message != test.want || result.Data["error"] != test.want {
				t.Fatalf("inconsistent error response: %#v", result)
			}
		})
	}
}
