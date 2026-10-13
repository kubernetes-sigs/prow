/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package githuboauth

import (
	"context"
	"encoding/gob"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/xsrftoken"
	"golang.org/x/oauth2"
)

const mockAccessToken = "justSomeRandomSecretToken"

type mockOAuthClient struct {
	config *oauth2.Config
}

func (c mockOAuthClient) WithFinalRedirectURL(path string) (OAuthClient, error) {
	parsedURL, err := url.Parse("www.something.com")
	if err != nil {
		return nil, err
	}
	q := parsedURL.Query()
	q.Set("dest", path)
	parsedURL.RawQuery = q.Encode()
	return mockOAuthClient{&oauth2.Config{RedirectURL: parsedURL.String()}}, nil
}

func (c mockOAuthClient) Exchange(ctx context.Context, code string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: mockAccessToken,
	}, nil
}

func (c mockOAuthClient) AuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string {
	return c.config.AuthCodeURL(state, opts...)
}

func getMockConfig(cookie *sessions.CookieStore) *Config {
	clientID := "mock-client-id"
	clientSecret := "mock-client-secret"
	redirectURL := "uni-test/redirect-url"
	scopes := []string{}

	return &Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,

		CookieStore: cookie,
	}
}

func createMockStateToken(config *Config) string {
	stateToken := xsrftoken.Generate(config.ClientSecret, "", "")
	state := hex.EncodeToString([]byte(stateToken))

	return state
}

func isEqual(token1 *oauth2.Token, token2 *oauth2.Token) bool {
	return token1.AccessToken == token2.AccessToken &&
		token1.Expiry == token2.Expiry &&
		token1.RefreshToken == token2.RefreshToken &&
		token1.TokenType == token2.TokenType
}

func TestHandleLogin(t *testing.T) {
	dest := "wherever"
	rerunStatus := "working"
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login?dest="+dest+"?rerun="+rerunStatus, nil)
	mockResponse := httptest.NewRecorder()

	handleLoginFn := mockAgent.HandleLogin(mockOAuthClient, false)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusFound {
		t.Errorf("Unexpected status code. Got %v, expected %v", result.StatusCode, http.StatusFound)
	}
	resultCookies := result.Cookies()
	var oauthCookie *http.Cookie
	for _, v := range resultCookies {
		if v.Name == oauthSessionCookie {
			oauthCookie = v
			break
		}
	}
	if oauthCookie == nil {
		t.Fatal("Cookie for oauth session not found")
		return
	}
	decodedCookie := make(map[interface{}]interface{})
	if err := securecookie.DecodeMulti(oauthCookie.Name, oauthCookie.Value, &decodedCookie, cookie.Codecs...); err != nil {
		t.Fatalf("Cannot decoded cookie: %v", err)
	}
	state, ok := decodedCookie[stateKey].(string)
	if !ok {
		t.Fatal("Error with getting state parameter")
	}
	stateTokenRaw, err := hex.DecodeString(state)
	if err != nil {
		t.Fatal("Cannot decoding state token")
	}
	stateToken := string(stateTokenRaw)
	if !xsrftoken.Valid(stateToken, mockConfig.ClientSecret, "", "") {
		t.Error("Expect the state token is valid, found state token invalid.")
	}
	if state == "" {
		t.Error("Expect state parameter is not empty, found empty")
	}
	destCount := 0
	path := mockResponse.Header().Get("Location")
	for _, q := range strings.Split(path, "&") {
		if q == "redirect_uri=www.something.com%3Fdest%3Dwherever%253Frerun%253Dworking" {
			destCount++
		}
	}
	if destCount != 1 {
		t.Errorf("Redirect URI in path does not include correct destination. path: %s, destination: %s", path, "?dest="+dest+"?rerun=working")
	}
}

func TestHandleLoginWithInvalidRedirectURL(t *testing.T) {
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockConfig.RedirectURL = "https://example.com/%"
	mockAgent := NewAgent(mockConfig, logrus.WithField("uni-test", "githuboauth"))
	oauthClient := NewClient(&oauth2.Config{RedirectURL: mockConfig.RedirectURL})

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login?dest=wherever", nil)
	mockResponse := httptest.NewRecorder()

	mockAgent.HandleLogin(oauthClient, false).ServeHTTP(mockResponse, mockRequest)
	if mockResponse.Code != http.StatusInternalServerError {
		t.Errorf("Unexpected status code. Got %v, expected %v", mockResponse.Code, http.StatusInternalServerError)
	}
	if location := mockResponse.Header().Get("Location"); location != "" {
		t.Errorf("Unexpected authorization redirect: %s", location)
	}
}

func TestHandleLogout(t *testing.T) {
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-logout", nil)
	_, err := cookie.New(mockRequest, tokenSession)
	if err != nil {
		t.Fatalf("Failed to create a mock token session with error: %v", err)
	}
	mockResponse := httptest.NewRecorder()

	handleLoginFn := mockAgent.HandleLogout(mockOAuthClient)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusFound {
		t.Errorf("Unexpected status code. Got %v, expected %v", result.StatusCode, http.StatusFound)
	}
	resultCookies := result.Cookies()
	var tokenCookie *http.Cookie
	cookieCounts := 0
	for _, v := range resultCookies {
		if v.Name == tokenSession {
			tokenCookie = v
			cookieCounts++
		}
	}
	if cookieCounts != 1 {
		t.Errorf("Wrong number of %s cookie. There should be only one cookie with name %s", tokenSession, tokenSession)
	}
	if tokenCookie == nil {
		t.Fatal("Cookie for oauth session not found")
		return
	}
	if tokenCookie.MaxAge != -1 {
		t.Errorf("Expect cookie MaxAge equals -1, %d", tokenCookie.MaxAge)
	}
}

func TestHandleLogoutWithLoginSession(t *testing.T) {
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-logout", nil)
	_, err := cookie.New(mockRequest, tokenSession)
	mocKLoginSession := &http.Cookie{
		Name: loginSession,
		Path: "/",
	}
	mockRequest.AddCookie(mocKLoginSession)
	if err != nil {
		t.Fatalf("Failed to create a mock token session with error: %v", err)
	}
	mockResponse := httptest.NewRecorder()

	handleLoginFn := mockAgent.HandleLogout(mockOAuthClient)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusFound {
		t.Errorf("Unexpected status code. Got %v, expected %v", result.StatusCode, http.StatusFound)
	}
	resultCookies := result.Cookies()
	var loginCookie *http.Cookie
	for _, v := range resultCookies {
		if v.Name == loginSession {
			loginCookie = v
			break
		}
	}
	if loginCookie == nil {
		t.Fatal("Cookie for oauth session not found")
		return
	}
	if loginCookie.MaxAge != -1 {
		t.Errorf("Expect cookie MaxAge equals -1, %d", loginCookie.MaxAge)
	}
}

type fakeAuthenticatedUserIdentifier struct {
	login string
}

func (a *fakeAuthenticatedUserIdentifier) LoginForRequester(requester, token string) (string, error) {
	return a.login, nil
}

func TestGetLogin(t *testing.T) {
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockToken := &oauth2.Token{AccessToken: "tokentokentoken"}
	mockRequest := httptest.NewRequest(http.MethodGet, "/someurl", nil)
	mockSession, err := sessions.GetRegistry(mockRequest).Get(cookie, "access-token-session")
	if err != nil {
		t.Fatalf("Error with getting mock session: %v", err)
	}
	mockSession.Values["access-token"] = mockToken

	login, err := mockAgent.GetLogin(mockRequest, &fakeAuthenticatedUserIdentifier{"correct-login"})
	if err != nil {
		t.Fatalf("Error getting login: %v", err)
	}
	if login != "correct-login" {
		t.Fatalf("Incorrect login: %s", login)
	}
}

func TestHandleRedirectWithInvalidState(t *testing.T) {
	gob.Register(&oauth2.Token{})
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}
	mockStateToken := createMockStateToken(mockConfig)

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login", nil)
	mockResponse := httptest.NewRecorder()
	query := mockRequest.URL.Query()
	query.Add("state", "bad-state-token")
	mockRequest.URL.RawQuery = query.Encode()
	mockSession, err := sessions.GetRegistry(mockRequest).Get(cookie, oauthSessionCookie)
	if err != nil {
		t.Fatalf("Error with getting mock session: %v", err)
	}
	mockSession.Values[stateKey] = mockStateToken

	handleLoginFn := mockAgent.HandleRedirect(mockOAuthClient, &fakeAuthenticatedUserIdentifier{""}, false)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()

	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("Invalid status code. Got %v, expected %v", result.StatusCode, http.StatusInternalServerError)
	}
}

func TestHandleRedirectWithValidState(t *testing.T) {
	gob.Register(&oauth2.Token{})
	cookie := sessions.NewCookieStore([]byte("secret-key"))
	mockConfig := getMockConfig(cookie)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockLogin := "foo_name"
	mockOAuthClient := mockOAuthClient{}
	mockStateToken := createMockStateToken(mockConfig)

	dest := "somewhere"
	rerunStatus := "working"
	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login?dest="+dest+"?rerun="+rerunStatus, nil)
	mockResponse := httptest.NewRecorder()
	query := mockRequest.URL.Query()
	query.Add("state", mockStateToken)
	query.Add("rerun", "working")
	mockRequest.URL.RawQuery = query.Encode()

	mockSession, err := sessions.GetRegistry(mockRequest).Get(cookie, oauthSessionCookie)
	if err != nil {
		t.Fatalf("Error with getting mock session: %v", err)
	}
	mockSession.Values[stateKey] = mockStateToken

	handleLoginFn := mockAgent.HandleRedirect(mockOAuthClient, &fakeAuthenticatedUserIdentifier{mockLogin}, false)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusFound {
		t.Errorf("Invalid status code. Got %v, expected %v", result.StatusCode, http.StatusFound)
	}
	resultCookies := result.Cookies()
	var oauthCookie *http.Cookie
	for _, v := range resultCookies {
		if v.Name == tokenSession {
			oauthCookie = v
			break
		}
	}
	if oauthCookie == nil {
		t.Fatalf("Cookie for oauth session not found")
		return
	}
	decodedCookie := make(map[interface{}]interface{})
	if err := securecookie.DecodeMulti(oauthCookie.Name, oauthCookie.Value, &decodedCookie, cookie.Codecs...); err != nil {
		t.Fatalf("Cannot decoded cookie: %v", err)
	}
	accessTokenFromCookie, ok := decodedCookie[tokenKey].(*oauth2.Token)
	if !ok {
		t.Fatalf("Error with getting access token: %v", decodedCookie)
	}
	token := &oauth2.Token{
		AccessToken: mockAccessToken,
	}
	if !isEqual(accessTokenFromCookie, token) {
		t.Errorf("Invalid access token. Got %v, expected %v", accessTokenFromCookie, token)
	}
	var loginCookie *http.Cookie
	for _, v := range resultCookies {
		if v.Name == loginSession {
			loginCookie = v
			break
		}
	}
	if loginCookie == nil {
		t.Fatalf("Cookie for github login not found")
		return
	}
	if loginCookie.Value != mockLogin {
		t.Errorf("Mismatch github login. Got %v, expected %v", loginCookie.Value, mockLogin)
	}
	path := mockResponse.Header().Get("Location")
	if path != "http://example.com/"+dest+"?rerun="+rerunStatus {
		t.Errorf("Incorrect final redirect URL. Actual path: %s, Expected path: /%s", path, dest+"?rerun="+rerunStatus)
	}
}

func newStaleCookie(t *testing.T, name string, values map[interface{}]interface{}) (*http.Cookie, *sessions.CookieStore) {
	t.Helper()
	oldStore := sessions.NewCookieStore([]byte("old-secret-key-for-signing-cookies"))
	newStore := sessions.NewCookieStore([]byte("new-secret-key-after-rotation!!!!"))

	staleReq := httptest.NewRequest(http.MethodGet, "/", nil)
	staleResp := httptest.NewRecorder()
	staleSession, err := oldStore.New(staleReq, name)
	if err != nil {
		t.Fatalf("Failed to create stale session: %v", err)
	}
	staleSession.Values = values
	if err := staleSession.Save(staleReq, staleResp); err != nil {
		t.Fatalf("Failed to save stale session: %v", err)
	}
	for _, cookie := range staleResp.Result().Cookies() {
		if cookie.Name == name {
			decodedCookie := make(map[interface{}]interface{})
			if err := securecookie.DecodeMulti(name, cookie.Value, &decodedCookie, newStore.Codecs...); err == nil {
				t.Fatal("Expected stale cookie to fail decoding with the current key")
			}
			return cookie, newStore
		}
	}
	t.Fatal("Failed to create stale cookie")
	return nil, nil
}

func TestHandleRedirectWithStaleCookie(t *testing.T) {
	gob.Register(&oauth2.Token{})
	staleCookie, newStore := newStaleCookie(t, tokenSession, map[interface{}]interface{}{
		tokenKey: &oauth2.Token{AccessToken: "expired-token"},
	})

	mockConfig := getMockConfig(newStore)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockLogin := "foo_name"
	mockOAuthClient := mockOAuthClient{}
	mockStateToken := createMockStateToken(mockConfig)

	dest := "somewhere"
	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login?dest="+dest, nil)
	// Attach the stale cookie -- this is what causes "the value is not valid"
	mockRequest.AddCookie(staleCookie)
	mockResponse := httptest.NewRecorder()
	query := mockRequest.URL.Query()
	query.Add("state", mockStateToken)
	mockRequest.URL.RawQuery = query.Encode()

	// Set up a valid OAuth state session using the new store
	mockSession, err := sessions.GetRegistry(mockRequest).Get(newStore, oauthSessionCookie)
	if err != nil {
		t.Fatalf("Error with getting mock session: %v", err)
	}
	mockSession.Values[stateKey] = mockStateToken

	handleRedirectFn := mockAgent.HandleRedirect(mockOAuthClient, &fakeAuthenticatedUserIdentifier{mockLogin}, false)
	handleRedirectFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()

	if result.StatusCode != http.StatusFound {
		t.Errorf("Expected redirect (302) despite stale cookie, got %v", result.StatusCode)
	}

	// Verify a new valid token cookie was issued
	var newTokenCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == tokenSession {
			newTokenCookie = c
			break
		}
	}
	if newTokenCookie == nil {
		t.Fatal("Expected a new token session cookie to be issued")
	}
	decodedCookie := make(map[interface{}]interface{})
	if err := securecookie.DecodeMulti(newTokenCookie.Name, newTokenCookie.Value, &decodedCookie, newStore.Codecs...); err != nil {
		t.Fatalf("Cannot decode new cookie with current key: %v", err)
	}
	accessToken, ok := decodedCookie[tokenKey].(*oauth2.Token)
	if !ok {
		t.Fatalf("Error with getting access token from new cookie: %v", decodedCookie)
	}
	if accessToken.AccessToken != mockAccessToken {
		t.Errorf("Invalid access token. Got %v, expected %v", accessToken.AccessToken, mockAccessToken)
	}
}

func TestHandleLoginWithStaleCookie(t *testing.T) {
	staleCookie, newStore := newStaleCookie(t, oauthSessionCookie, map[interface{}]interface{}{
		stateKey: "old-state",
	})

	mockConfig := getMockConfig(newStore)
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login?dest=somewhere", nil)
	mockRequest.AddCookie(staleCookie)
	mockResponse := httptest.NewRecorder()

	handleLoginFn := mockAgent.HandleLogin(mockOAuthClient, false)
	handleLoginFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()

	if result.StatusCode != http.StatusFound {
		t.Errorf("Expected redirect (302) despite stale cookie, got %v", result.StatusCode)
	}
	var replacementCookie *http.Cookie
	for _, cookie := range result.Cookies() {
		if cookie.Name == oauthSessionCookie {
			replacementCookie = cookie
			break
		}
	}
	if replacementCookie == nil {
		t.Fatal("Expected a replacement OAuth session cookie")
	}
	decodedCookie := make(map[interface{}]interface{})
	if err := securecookie.DecodeMulti(replacementCookie.Name, replacementCookie.Value, &decodedCookie, newStore.Codecs...); err != nil {
		t.Fatalf("Cannot decode replacement cookie with current key: %v", err)
	}
	state, ok := decodedCookie[stateKey].(string)
	if !ok || state == "" {
		t.Fatalf("Expected a nonempty state in the replacement cookie, got %v", decodedCookie[stateKey])
	}
	stateToken, err := hex.DecodeString(state)
	if err != nil {
		t.Fatalf("Cannot decode replacement state token: %v", err)
	}
	if !xsrftoken.Valid(string(stateToken), mockConfig.ClientSecret, "", "") {
		t.Error("Expected a valid replacement state token")
	}
	redirectURL, err := url.Parse(result.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Cannot parse authorization redirect: %v", err)
	}
	if redirectState := redirectURL.Query().Get("state"); redirectState != state {
		t.Errorf("Authorization redirect state %q does not match replacement cookie state %q", redirectState, state)
	}
}

func TestHandleLogoutWithStaleCookie(t *testing.T) {
	staleCookie, newStore := newStaleCookie(t, tokenSession, map[interface{}]interface{}{
		tokenKey: "expired-token",
	})
	mockAgent := NewAgent(getMockConfig(newStore), logrus.WithField("uni-test", "githuboauth"))

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-logout", nil)
	mockRequest.AddCookie(staleCookie)
	mockRequest.AddCookie(&http.Cookie{Name: loginSession, Value: "foo_name", Path: "/"})
	if _, err := newStore.New(mockRequest, tokenSession); err == nil {
		t.Fatal("Expected stale token cookie to fail decoding with the current key")
	}
	mockResponse := httptest.NewRecorder()
	mockAgent.HandleLogout(mockOAuthClient{}).ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusFound {
		t.Errorf("Expected redirect (302) despite stale cookie, got %v", result.StatusCode)
	}
	for _, name := range []string{tokenSession, loginSession} {
		var expiredCookie *http.Cookie
		for _, cookie := range result.Cookies() {
			if cookie.Name == name {
				expiredCookie = cookie
				break
			}
		}
		if expiredCookie == nil {
			t.Fatalf("Expected expired %s cookie", name)
		}
		if expiredCookie.MaxAge != -1 {
			t.Errorf("Expected %s cookie MaxAge to be -1, got %d", name, expiredCookie.MaxAge)
		}
		if expiredCookie.Expires.IsZero() || !expiredCookie.Expires.Before(time.Now()) {
			t.Errorf("Expected %s cookie expiration in the past, got %v", name, expiredCookie.Expires)
		}
	}
}

func TestHandleLoginWithStaleCookieSaveFailure(t *testing.T) {
	staleCookie, newStore := newStaleCookie(t, oauthSessionCookie, map[interface{}]interface{}{
		stateKey: "old-state",
	})
	mockAgent := NewAgent(getMockConfig(newStore), logrus.WithField("uni-test", "githuboauth"))

	// Permit the stale cookie's size so decoding fails on its old signature,
	// but reject the larger replacement containing a freshly generated state.
	newStore.Codecs[0].(*securecookie.SecureCookie).MaxLength(len(staleCookie.Value))
	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login", nil)
	mockRequest.AddCookie(staleCookie)
	if _, err := newStore.New(mockRequest, oauthSessionCookie); err == nil {
		t.Fatal("Expected stale OAuth cookie to fail decoding with the current key")
	}
	mockResponse := httptest.NewRecorder()
	mockAgent.HandleLogin(mockOAuthClient{}, false).ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()
	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500 when saving the replacement session fails, got %v", result.StatusCode)
	}
	if body := mockResponse.Body.String(); !strings.Contains(body, "Save oauth session") || !strings.Contains(body, "value is too long") {
		t.Errorf("Expected a session encoding size error after recovering from the stale cookie, got %q", body)
	}
	if location := result.Header.Get("Location"); location != "" {
		t.Errorf("Expected no redirect after saving fails, got %q", location)
	}
	if cookies := result.Cookies(); len(cookies) != 0 {
		t.Errorf("Expected no cookies after encoding fails, got %v", cookies)
	}
}

func TestHandleRedirectWithStaleOAuthSessionCookie(t *testing.T) {
	mockConfig := getMockConfig(nil)
	mockStateToken := createMockStateToken(mockConfig)
	staleCookie, newStore := newStaleCookie(t, oauthSessionCookie, map[interface{}]interface{}{
		stateKey: mockStateToken,
	})
	mockConfig.CookieStore = newStore
	mockLogger := logrus.WithField("uni-test", "githuboauth")
	mockAgent := NewAgent(mockConfig, mockLogger)
	mockOAuthClient := mockOAuthClient{}

	mockRequest := httptest.NewRequest(http.MethodGet, "/mock-login", nil)
	mockRequest.AddCookie(staleCookie)
	query := mockRequest.URL.Query()
	query.Add("state", mockStateToken)
	mockRequest.URL.RawQuery = query.Encode()
	mockResponse := httptest.NewRecorder()

	handleRedirectFn := mockAgent.HandleRedirect(mockOAuthClient, &fakeAuthenticatedUserIdentifier{""}, false)
	handleRedirectFn.ServeHTTP(mockResponse, mockRequest)
	result := mockResponse.Result()

	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500 when OAuth state cookie cannot be decoded, got %v", result.StatusCode)
	}
}
