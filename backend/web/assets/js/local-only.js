(function () {
    'use strict';

    function showUnavailable(isForm) {
        let status = document.getElementById('local-only-status');
        if (!status) {
            status = document.createElement('div');
            status.id = 'local-only-status';
            status.className = 'osmuk-inset-text';
            status.setAttribute('role', 'status');
            const container = document.querySelector('main') || document.body;
            container.prepend(status);
        }
        status.textContent = isForm
            ? 'This action is not connected yet. No information has been sent.'
            : 'This external page is unavailable in the local preview.';
        status.setAttribute('tabindex', '-1');
        status.focus();
    }

    function isExternal(url) {
        const resolved = new URL(url, window.location.href);
        return resolved.protocol !== window.location.protocol || resolved.origin !== window.location.origin;
    }

    // Capture before component handlers so blocked controls cannot trigger AJAX.
    document.addEventListener('click', function (event) {
        const link = event.target.closest('a');
        if (!link) return;
        if (link.hasAttribute('data-local-unavailable') || isExternal(link.href)) {
            event.preventDefault();
            event.stopImmediatePropagation();
            showUnavailable(false);
        }
    }, true);

    document.addEventListener('submit', function (event) {
        const form = event.target;
        if (form.hasAttribute('data-local-unavailable') || isExternal(form.action)) {
            event.preventDefault();
            event.stopImmediatePropagation();
            showUnavailable(true);
        }
    }, true);
}());
