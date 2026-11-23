(function () {
  'use strict';
  const form = document.querySelector('[data-person-form]');
  if (!form) return;
  const nationality = form.querySelector('#nationality');
  if (nationality) {
    nationality.maxLength = 4;
    nationality.pattern = '[A-Za-z]{3,4}';
    nationality.title = 'Enter a 3 or 4 letter nationality code, such as IND or USA';
    nationality.placeholder = 'For example, IND';
    nationality.addEventListener('input', () => { nationality.value = nationality.value.toUpperCase(); });
  }
  const entries = form.querySelector('#entries_allowed');
  if (entries) {
    entries.type = 'text';
    entries.placeholder = 'For example, 2 or Multiple';
    entries.addEventListener('change', () => {
      entries.value = entries.value.trim();
      if (entries.value.toLowerCase() === 'multiple') entries.value = 'Multiple';
    });
  }
  const insurance = form.querySelector('#national_insurance_number');
  if (insurance) insurance.placeholder = 'N/A if not applicable';
  const accompanied = form.querySelector('#accompanied_by');
  if (accompanied) { accompanied.maxLength = 2000; accompanied.placeholder = 'Add any accompanying-person details'; }
  form.querySelectorAll('input[type="date"]').forEach(input => input.classList.add('date-picker'));
  const file = form.querySelector('[data-photo-file]');
  const controls = form.querySelector('[data-photo-controls]');
  const canvas = controls.querySelector('canvas');
  const status = form.querySelector('[data-photo-status]');
  const zoom = form.querySelector('[data-photo-zoom]');
  const x = form.querySelector('[data-photo-x]');
  const y = form.querySelector('[data-photo-y]');
  let bitmap, generation = 0, loading = false, saving = false, ready = false;
  function draw() {
    if (!bitmap) return;
    const width = Math.min(bitmap.width, bitmap.height * 7 / 9) / Number(zoom.value);
    const height = width * 9 / 7;
    const ctx = canvas.getContext('2d');
    ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, 700, 900);
    ctx.drawImage(bitmap, (bitmap.width - width) * Number(x.value) / 100,
      (bitmap.height - height) * Number(y.value) / 100, width, height, 0, 0, 700, 900);
  }
  function reset() { zoom.value = '1'; x.value = y.value = '50'; draw(); }
  file.addEventListener('change', async function () {
    const current = ++generation;
    if (bitmap) bitmap.close();
    bitmap = null; controls.hidden = true; file.setCustomValidity(''); status.textContent = ''; ready = false;
    const selected = file.files[0];
    loading = !!selected;
    if (!selected) return;
    try {
      if (!['image/jpeg', 'image/png'].includes(selected.type) || selected.size > 5 * 1024 * 1024)
        throw new Error('Choose a JPEG or PNG no larger than 5 MB.');
      const image = await createImageBitmap(selected);
      if (current !== generation) { image.close(); return; }
      if (image.width > 4000 || image.height > 4000) { image.close(); throw new Error('Photo dimensions must not exceed 4000 × 4000.'); }
      bitmap = image; controls.hidden = false; reset();
      status.textContent = 'Photo selected. Adjust the crop, then save the record.';
    } catch (error) {
      if (current !== generation) return;
      file.setCustomValidity(error.message); status.textContent = error.message;
    } finally { if (current === generation) loading = false; }
  });
  [zoom, x, y].forEach(input => input.addEventListener('input', draw));
  form.querySelector('[data-photo-reset]').addEventListener('click', reset);
  form.querySelector('[data-photo-clear]').addEventListener('click', function () {
    file.value = ''; file.dispatchEvent(new Event('change'));
  });
  form.addEventListener('submit', async function (event) {
    if (ready || !file.files.length) return;
    event.preventDefault();
    if (loading || saving || !bitmap || !form.reportValidity()) return;
    const current = generation;
    saving = true; status.textContent = 'Preparing cropped photo…';
    try {
      const blob = await new Promise(resolve => canvas.toBlob(resolve, 'image/jpeg', 0.9));
      if (current !== generation) return;
      if (!blob) throw new Error('Could not crop the image. Please select it again.');
      const data = new DataTransfer();
      data.items.add(new File([blob], 'portrait.jpg', { type: 'image/jpeg' }));
      file.files = data.files; ready = true; form.requestSubmit(event.submitter || undefined);
    } catch (error) { status.textContent = error.message; }
    finally { saving = false; }
  });
}());
