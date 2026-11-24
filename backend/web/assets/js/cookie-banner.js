(function () {
    'use strict';

    const storageKey = 'local-site-cookie-choice';
    const bannerSelector = '.osmuk-cookie-banner, .govuk-cookie-banner';
    const acceptSelector = '[data-accept-cookies], .cc-accept';
    const rejectSelector = '[data-reject-cookies], .cc-reject';
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
        document.querySelectorAll(bannerSelector).forEach(function (banner) {
            setVisible(banner, !decided);
            Array.from(banner.children).forEach(function (message) {
                if (!message.classList.contains('osmuk-cookie-banner__message') && !message.classList.contains('govuk-cookie-banner__message')) return;
                const isConfirmation = message.classList.contains('cc-show-on-accept') || message.classList.contains('cc-show-on-reject');
                setVisible(message, !decided && !isConfirmation);
            });
        });
    }

    // Delegate to the document so newly rendered banners work too.
    document.addEventListener('click', function (event) {
        const target = event.target instanceof Element ? event.target : event.target.parentElement;
        if (!target) return;
        const button = target.closest(acceptSelector + ', ' + rejectSelector);
        if (!button || !button.closest(bannerSelector)) return;
        event.preventDefault();
        event.stopImmediatePropagation();
        currentChoice = button.matches(acceptSelector) ? 'accepted' : 'rejected';
        // Hide immediately, even if browser storage is blocked.
        updateBanners();
        try {
            localStorage.setItem(storageKey, currentChoice);
        } catch (_) {
            // The in-memory choice still applies for this page.
        }
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
    }, true);

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', updateBanners, { once: true });
    }

    updateBanners();
    window.addEventListener('pageshow', function (event) {
        if (event.persisted) {
            currentChoice = readChoice() || currentChoice;
            updateBanners();
        }
    });
    window.addEventListener('storage', function (event) {
        if (event.key === storageKey || event.key === null) {
            currentChoice = readChoice();
            updateBanners();
        }
    });
}());
