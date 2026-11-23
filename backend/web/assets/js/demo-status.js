(function () {
    'use strict';
    const button = document.getElementById('rotateImage');
    const photo = document.getElementById('photo');
    if (!button || !photo) return;
    let turns = 0;
    button.addEventListener('click', function () {
        turns = (turns + 1) % 4;
        const scale = turns % 2 ? Math.min(photo.clientWidth / photo.clientHeight, photo.clientHeight / photo.clientWidth) : 1;
        photo.style.transform = 'rotate(' + (turns * 90) + 'deg) scale(' + scale + ')';
    });
}());
