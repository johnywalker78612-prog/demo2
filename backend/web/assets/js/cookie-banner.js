(function () {
    'use strict';

    const storageKey = 'local-site-cookie-choice';
    const banners = document.querySelectorAll('.osmuk-cookie-banner, .govuk-cookie-banner');
    const acceptSelector = '[data-accept-cookies], .cc-accept';
    const rejectSelector = '[data-reject-cookies], .cc-reject';
    const hideSelector = '[data-hide-cookie-banner], .cc-action-hide';
    let pendingChoice = null;
    let currentChoice = readChoice();

    function readChoice() {
        try {
            return localStorage.getItem(storageKey);
        } catch (_) {
            return null;
        }
    }

    function setVisible(element, visible) {
        element.hidden = !visible;
        if (visible) {
            element.style.removeProperty('display');
        } else {
            element.style.setProperty('display', 'none', 'important');
        }
    }

    function updateBanners() {
        const decided = currentChoice === 'accepted' || currentChoice === 'rejected';
        banners.forEach(function (banner) {
            setVisible(banner, !decided || pendingChoice !== null);
            Array.from(banner.children).forEach(function (message) {
                if (!message.classList.contains('osmuk-cookie-banner__message') && !message.classList.contains('govuk-cookie-banner__message')) return;
                if (message.classList.contains('cc-show-on-accept')) {
                    setVisible(message, pendingChoice === 'accepted');
                } else if (message.classList.contains('cc-show-on-reject')) {
                    setVisible(message, pendingChoice === 'rejected');
                } else {
                    setVisible(message, !decided);
                }
            });
        });
    }

    banners.forEach(function (banner) {
        banner.addEventListener('click', function (event) {
            const button = event.target.closest(acceptSelector + ', ' + rejectSelector + ', ' + hideSelector);
            if (!button || !banner.contains(button)) return;
            event.preventDefault();
            event.stopImmediatePropagation();
            if (button.matches(hideSelector)) {
                pendingChoice = null;
                updateBanners();
                const nextContent = document.querySelector('main, [role="main"], h1');
                if (nextContent) {
                    if (!nextContent.hasAttribute('tabindex')) {
                        nextContent.setAttribute('tabindex', '-1');
                        nextContent.addEventListener('blur', function () {
                            nextContent.removeAttribute('tabindex');
                        }, { once: true });
                    }
                    nextContent.focus({ preventScroll: true });
                }
                return;
            }
            currentChoice = button.matches(acceptSelector) ? 'accepted' : 'rejected';
            pendingChoice = currentChoice;
            try {
                localStorage.setItem(storageKey, currentChoice);
            } catch (_) {
                // The confirmation still works when browser storage is unavailable.
            }
            updateBanners();
            const confirmation = banner.querySelector(currentChoice === 'accepted' ? '.cc-show-on-accept' : '.cc-show-on-reject');
            confirmation.setAttribute('tabindex', '-1');
            confirmation.focus({ preventScroll: true });
        }, true);
    });

    updateBanners();
    window.addEventListener('pageshow', function (event) {
        if (event.persisted) {
            currentChoice = readChoice() || currentChoice;
            pendingChoice = null;
            updateBanners();
        }
    });
    window.addEventListener('storage', function (event) {
        if (event.key === storageKey || event.key === null) {
            currentChoice = readChoice();
            pendingChoice = null;
            updateBanners();
        }
    });
}());
