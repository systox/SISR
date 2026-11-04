package cef

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/Alia5/SISR/cmd"
	"github.com/Alia5/SISR/sdl"
	"github.com/Alia5/SISR/sdl/extras"
	"github.com/Alia5/SISR/webview"
	"github.com/danielgtaylor/huma/v2"
)

func registerOverlayStateChanged(a huma.API, c *cmd.SISRContext) {
	huma.Register(a, huma.Operation{
		Method:      http.MethodPost,
		Path:        "/api/v1/steam/cef/overlay-state-changed",
		Description: "Callback for CEF Injectee when Steam overlay is opened/closed",
		Tags:        []string{"steam", "cef", "callback"},
	}, overlayStateChanged(c))
}

type OverlayChangedRequest struct {
	Body OverlayChanged
}

type OverlayChanged struct {
	Open bool `json:"open"`

	// At is when the injectee observed the change (ms since epoch, sub-ms precision).
	// Callbacks are fired without awaiting each other, so they can arrive out of order;
	// a callback older than the last one handled is ignored.
	At float64 `json:"at,omitempty"`
}

func overlayStateChanged(c *cmd.SISRContext) func(ctx context.Context, req *OverlayChangedRequest) (*struct{}, error) {

	previousVisibility := false
	previousHitTest := false
	var mtx sync.Mutex
	var lastAt float64

	return func(ctx context.Context, req *OverlayChangedRequest) (*struct{}, error) {
		// The lock covers ordering only: the stale check, the layout switch and
		// queueing the window update. Waiting for the main loop happens outside
		// it, so a slow dispatch can't hold up the next callback.
		mtx.Lock()

		if req.Body.At > 0 {
			if req.Body.At <= lastAt {
				mtx.Unlock()
				slog.Debug("Ignoring stale overlay state change",
					"open", req.Body.Open, "at", req.Body.At, "lastAt", lastAt)
				return nil, nil
			}
			lastAt = req.Body.At
		}

		if c.BindingEnforcer != nil {
			var err error
			if req.Body.Open {
				err = c.BindingEnforcer.Suspend()
			} else {
				err = c.BindingEnforcer.Resume()
			}
			if err != nil {
				slog.Error("Failed to update SteamInput layout for overlay state", "open", req.Body.Open, "error", err)
			}
		}

		c.Config.Lock()
		kbmEnabled := c.Config.KeyboardMouseEmulation
		c.Config.Unlock()

		resCh := c.WindowDispatcher.Schedule(func(w *sdl.Window, wv webview.WebView) any {
			if req.Body.Open {
				previousVisibility = wv.Visible()
				previousHitTest = extras.GetWindowHitTest(w)
			}

			slog.Debug("Overlay state changed",
				"previousWebViewVisibility", previousVisibility,
				"previousCursorHitTest", previousHitTest,
				"newOverlayOpenState", req.Body.Open,
			)

			if req.Body.Open {
				wv.SetVisible(false)
				err := extras.SetCursorHitTest(w, true)
				if err != nil {
					slog.Error("Failed setting window cursor hittest", "error", err)
				}
			} else {
				if previousVisibility {
					wv.SetVisible(true)
				}
				targetHitTest := previousHitTest
				if kbmEnabled {
					targetHitTest = true
				}
				err := extras.SetCursorHitTest(w, targetHitTest)
				if err != nil {
					slog.Error("Failed setting window cursor hittest", "error", err)
				}
			}
			return nil
		})
		mtx.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case _, ok := <-resCh:
			if !ok {
				return nil, cmd.ErrDispatcherClosed
			}
		}
		return nil, nil
	}
}
