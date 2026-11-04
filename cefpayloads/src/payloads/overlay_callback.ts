import { client } from '$lib/api/client';

__INJECT_RETURN = (async () => {
    // Injected into "normal" overlay-tab or SharedJSContext

    if (!window.__sisrCleanup) {
        window.__sisrCleanup = {};
    }
    const steamClientOverlay = window.SteamClient?.Overlay ?? (opener as SteamWindow)?.SteamClient?.Overlay;

    if (window.__sisrCleanup.overlayCallback) {
        window.__sisrCleanup.overlayCallback();
    }

    if (steamClientOverlay) {
        const unregister = await steamClientOverlay.RegisterForOverlayActivated(
            (_a, _b, overlayOpen) => {
                void client.POST('/api/v1/steam/cef/overlay-state-changed', {
                    body: {
                        open: overlayOpen,
                        at: performance.timeOrigin + performance.now()
                    }
                }).catch((e) => console.error('Error sending overlay state change', e));
            }
        ) as { unregister: () => void } ;

        window.__sisrCleanup.overlayCallback = () => {
            unregister.unregister?.();
            delete window.__sisrCleanup?.overlayCallback;
        };
    } else {
        // Injected into "Gaming Mode", no overlay tab exists,
        // but we can query focus of the big picture menu ;)

        const focusListener = () => {
            void client.POST('/api/v1/steam/cef/overlay-state-changed', {
                body: {
                    open: document.hasFocus(),
                    at: performance.timeOrigin + performance.now()
                }
            }).catch((e) => console.error('Error sending overlay state change', e));
        };
        const focusOutListener = () => {
            void client.POST('/api/v1/steam/cef/overlay-state-changed', {
                body: {
                    open: false,
                    at: performance.timeOrigin + performance.now()
                }
            }).catch((e) => console.error('Error sending overlay state change', e));
        };

        window.addEventListener('focus', focusListener);
        window.addEventListener('focusout', focusOutListener);
        window.__sisrCleanup.overlayCallback = () => {
            window.removeEventListener('focus', focusListener);
            window.removeEventListener('focusout', focusOutListener);
            delete window.__sisrCleanup?.overlayCallback;
        };
    }
})();
