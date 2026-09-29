// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Prompt shows the user code and verification URL on the local console.
type Prompt func(userCode, verificationURI string)

// DeviceLogin runs the OAuth device flow for a GitHub App's (non-secret) client ID and
// returns a user-to-server token. The token is returned, never written anywhere: the
// install medium holds no credential, before or after.
//
// GitHub reports device-flow errors as JSON with a 200 or 4xx status, so the body is
// always parsed first (the same lesson as every other device-flow client).
func (c *Client) DeviceLogin(ctx context.Context, clientID string, prompt Prompt) (string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "", errors.New("no GitHub App client ID: pass --client-id or river.guide.client_id= (it is not a secret)")
	}
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	if err := c.form(ctx, c.Web+"/login/device/code", url.Values{"client_id": {clientID}}, &dc); err != nil {
		return "", fmt.Errorf("request device code: %w", err)
	}
	if dc.Error != "" {
		return "", fmt.Errorf("device code rejected (%s): %s", dc.Error, dc.ErrorDesc)
	}
	if dc.DeviceCode == "" || dc.UserCode == "" {
		return "", errors.New("GitHub returned no device code")
	}
	if prompt != nil {
		prompt(dc.UserCode, dc.VerificationURI)
	}
	interval := time.Duration(max(dc.Interval, 5)) * time.Second
	if c.pollOverride > 0 {
		interval = c.pollOverride
	}
	deadline := time.Now().Add(time.Duration(max(dc.ExpiresIn, 60)) * time.Second)
	failures := 0
	for {
		if time.Now().After(deadline) {
			return "", errors.New("device code expired before it was authorized")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		var res struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			ErrorDesc   string `json:"error_description"`
		}
		err := c.form(ctx, c.Web+"/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &res)
		if err != nil {
			if failures++; failures > 10 {
				return "", fmt.Errorf("poll for token: %w", err)
			}
			continue
		}
		failures = 0
		switch res.Error {
		case "":
			if res.AccessToken == "" {
				return "", errors.New("GitHub reported success without a token")
			}
			return res.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			return "", errors.New("device code expired")
		case "access_denied":
			return "", errors.New("authorization was denied")
		default:
			return "", fmt.Errorf("device flow error %q: %s", res.Error, res.ErrorDesc)
		}
	}
}

// SetPollInterval overrides the device-flow poll interval (tests).
func (c *Client) SetPollInterval(d time.Duration) { c.pollOverride = d }

func (c *Client) form(ctx context.Context, endpoint string, v url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return explainDial(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %s", endpoint, resp.Status)
	}
	return nil
}
