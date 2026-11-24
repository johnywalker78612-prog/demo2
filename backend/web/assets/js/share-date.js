(function () {
    'use strict';
    const date = document.getElementById('today-date');
    if (date) {
        date.textContent = new Date().toLocaleDateString('en-GB', {
            day: 'numeric', month: 'long', year: 'numeric'
        });
    }
}());
