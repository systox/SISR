package steaminputbindings

import (
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/Alia5/SISR/helper"
)

type Enforcer interface {
	// GetForcedInputAppID returns the requested forced appID (0 = not forced),
	// even while enforcement is suspended.
	GetForcedInputAppID() uint32
	ForceInputAppID(appID uint32) error
	ForceOwnAppID() error
	// Suspend swaps the forced layout for Steam's own menu layout while the Steam
	// overlay is open, without forgetting it. Merely unforcing is not enough: Steam
	// picks the overlay layout when the Steam button is pressed, before SISR hears
	// that the overlay opened, and does not re-pick on unforce. Force requests
	// made while suspended are recorded and applied on Resume.
	Suspend() error
	// Resume re-applies the requested layout after Suspend.
	Resume() error
	// Shutdown unforces the layout and ignores every later request.
	Shutdown() error
}

type enforcer struct {
	forcedAppID uint32
	ownAppID    uint32
	noSteam     bool
	suspended   bool
	menuForced  bool // Steam's menu layout is forced in place of forcedAppID
	closed      bool

	mtx sync.Mutex
}

func NewEnforcer(noSteam bool) Enforcer {
	var ownAppID uint32
	ownAppIDStr := os.Getenv("SteamAppId")
	if ownAppIDStr == "" || ownAppIDStr == "0" {
		ownAppIDStr = os.Getenv("SISR_MARKER_ID")
	}
	if ownAppIDStr != "" {
		appID64, err := strconv.ParseUint(ownAppIDStr, 10, 32)
		if err != nil {
			slog.Error("Failed to parse steamAppIDEnv", "SteamAppID", ownAppIDStr, "error", err)
		} else {
			ownAppID = uint32(appID64)
		}
	}
	return &enforcer{
		ownAppID: ownAppID,
		noSteam:  noSteam,
	}
}

// steamUIAppID is the appID Steam itself uses for its menu UI ("ClientUI").
// Forcing it loads Steam's menu-navigation layout (basicui), the same one Steam
// picks on its own when the menu opens over an ordinary game.
const steamUIAppID = 769

func openForceURL(appID uint32) error {
	return helper.OpenURL("steam://forceinputappid/" + strconv.FormatUint(uint64(appID), 10))
}

func (e *enforcer) GetForcedInputAppID() uint32 {
	e.mtx.Lock()
	defer e.mtx.Unlock()
	return e.forcedAppID
}

func (e *enforcer) ForceInputAppID(appID uint32) error {
	e.mtx.Lock()
	defer e.mtx.Unlock()

	if e.noSteam || e.closed {
		return nil
	}

	if appID == 0 {
		slog.Info("Unforcing SteamInput layout", "suspended", e.suspended)
	} else {
		slog.Info("Forcing SteamInput layout for appID", "appID", appID, "suspended", e.suspended)
	}

	return e.setLocked(appID)
}

func (e *enforcer) ForceOwnAppID() error {
	e.mtx.Lock()
	defer e.mtx.Unlock()

	if e.noSteam || e.closed {
		return nil
	}

	if e.ownAppID == 0 {
		return ErrNoSteamAppID
	}

	slog.Info("Forcing SteamInput layout for own appID", "appID", e.ownAppID, "suspended", e.suspended)

	return e.setLocked(e.ownAppID)
}

// setLocked records the requested appID and applies it unless suspended.
// Callers must hold e.mtx.
func (e *enforcer) setLocked(appID uint32) error {
	if !e.suspended {
		err := openForceURL(appID)
		if err != nil {
			return err
		}
	}
	e.forcedAppID = appID
	return nil
}

func (e *enforcer) Suspend() error {
	e.mtx.Lock()
	defer e.mtx.Unlock()

	if e.noSteam || e.closed || e.suspended {
		return nil
	}
	e.suspended = true
	if e.forcedAppID == 0 {
		// Nothing forced: Steam picks its menu layout on its own.
		return nil
	}
	slog.Info("Suspending forced SteamInput layout for Steam menu layout", "appID", e.forcedAppID, "menuAppID", steamUIAppID)
	err := openForceURL(steamUIAppID)
	if err != nil {
		return err
	}
	e.menuForced = true
	return nil
}

func (e *enforcer) Resume() error {
	e.mtx.Lock()
	defer e.mtx.Unlock()

	if e.noSteam || e.closed || !e.suspended {
		return nil
	}
	e.suspended = false
	// Nothing forced before or during the overlay: Steam needs no change.
	if e.forcedAppID == 0 && !e.menuForced {
		return nil
	}
	slog.Info("Resuming forced SteamInput layout", "appID", e.forcedAppID)
	var err error
	if e.forcedAppID == 0 {
		err = e.unforceMenuLocked()
	} else {
		err = openForceURL(e.forcedAppID)
	}
	if err != nil {
		// Keep menuForced, so the next overlay close retries.
		return err
	}
	e.menuForced = false
	return nil
}

// unforceMenuLocked lifts a forced menu layout. A bare unforce leaves Steam on the
// menu layout until the next focus change, so it first forces SISR's own layout,
// which Steam would pick for SISR's window anyway. Callers must hold e.mtx.
func (e *enforcer) unforceMenuLocked() error {
	if e.ownAppID != 0 {
		err := openForceURL(e.ownAppID)
		if err != nil {
			return err
		}
	}
	return openForceURL(0)
}

func (e *enforcer) Shutdown() error {
	e.mtx.Lock()
	defer e.mtx.Unlock()

	if e.noSteam || e.closed {
		return nil
	}
	e.closed = true
	// No unforceMenuLocked here: SISR's window closing is a focus change, so
	// Steam re-picks the layout on its own.
	return openForceURL(0)
}
