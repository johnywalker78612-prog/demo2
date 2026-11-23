(function () {
    'use strict';

    // Replace this adapter with the future email API integration. The server
    // should resolve the recipient from its session, not from page text.
    // Resolve { sent: true } only after the server confirms delivery was requested.
    window.securityCodeDelivery = window.securityCodeDelivery || {
        resend: async function () {
            return { sent: false };
        }
    };

    const link = document.getElementById('resend-security-code');
    const status = document.getElementById('resend-security-code-status');
    if (!link || !status) return;
    let pending = false;

    link.addEventListener('click', async function (event) {
        event.preventDefault();
        if (pending) return;
        pending = true;
        link.setAttribute('aria-disabled', 'true');
        status.hidden = false;
        status.textContent = 'Requesting a new security code…';
        try {
            const result = await window.securityCodeDelivery.resend();
            status.textContent = result && result.sent === true
                ? 'A new security code has been requested. Check your email.'
                : 'Email delivery is not available yet. No code has been sent.';
        } catch (_) {
            status.textContent = 'We could not resend the security code. Please try again.';
        } finally {
            pending = false;
            link.removeAttribute('aria-disabled');
        }
    });
}());
