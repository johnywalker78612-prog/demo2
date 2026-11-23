(function () {
    'use strict';
    const form = document.querySelector('form[data-demo-next]');
    if (!form) return;
    function advance(event) {
        event.preventDefault();
        form.querySelectorAll('input[type="text"]').forEach(function (input) {
            input.value = input.value.trim();
        });
        if (!form.reportValidity()) return;
        const next = form.dataset.demoNext;
        if (['/gov/passport','/gov/date-of-birth','/gov/security-code-delivery','/gov/security-code'].includes(location.pathname)) {
            const data = new URLSearchParams();
            form.querySelectorAll('input').forEach(function (input) { if (input.name) data.set(input.name, input.value); });
            const id = document.getElementById('documentNumber');
            if (id) data.set('documentNumber', id.value);
            const day = document.getElementById('dob-day'), month = document.getElementById('dob-month'), year = document.getElementById('dob-year');
            if (day && month && year) data.set('date_of_birth', year.value + '-' + month.value.padStart(2,'0') + '-' + day.value.padStart(2,'0'));
            const code = document.getElementById('verificationCode');
            if (code) data.set('verificationCode', code.value);
            fetch(location.pathname, {method:'POST', headers:{'Content-Type':'application/x-www-form-urlencoded'}, body:data}).then(function (response) { if (response.redirected) location.assign(response.url); else return response.text().then(function (text) { document.open(); document.write(text); document.close(); }); });
            return;
        }
        if (next) {
            // No form serialization, URL parameters, storage or network API call.
            window.location.assign(next);
            return;
        }
        let status = document.getElementById('demo-complete');
        if (!status) {
            status = document.createElement('p');
            status.id = 'demo-complete';
            status.className = 'osmuk-inset-text';
            status.setAttribute('role', 'status');
            form.appendChild(status);
        }
        status.textContent = 'End of this demo. No code was verified or sent. The next page has not been added yet.';
    }
    form.addEventListener('submit', advance);
    form.querySelector('[data-demo-continue]').addEventListener('click', advance);
}());
