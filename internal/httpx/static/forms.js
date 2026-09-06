// Progressive enhancement for the availability grid and the override toggles.
// Everything here is optional: with JavaScript off the grid is still a plain
// set of text inputs and the buffer fields are still editable, so the form
// submits exactly the same values either way.
(function () {
  'use strict';

  var WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri'];
  var ALL_DAYS = WEEKDAYS.concat(['sat', 'sun']);

  // ---------------------------------------------------------------------
  // Quick fill: type one range, drop it into a whole column.
  // ---------------------------------------------------------------------

  function fill(button) {
    var box = button.closest('[data-hours-fill]');
    var form = button.closest('form');
    if (!box || !form) return;

    var mode = button.getAttribute('data-fill-apply');
    var source = box.querySelector('[data-fill-value]');
    var prefix = box.getAttribute('data-hours-fill');
    var value = mode === 'clear' ? '' : (source ? source.value.trim() : '');
    var days = mode === 'weekdays' ? WEEKDAYS : ALL_DAYS;

    days.forEach(function (day) {
      var input = form.elements[prefix + '_' + day];
      if (input) input.value = value;
    });
    if (mode === 'clear' && source) source.value = '';
  }

  document.addEventListener('click', function (ev) {
    var button = ev.target.closest('[data-fill-apply]');
    if (button) fill(button);
  });

  // Enter in a quick-fill box means "apply this", not "submit the form" —
  // submitting a half-filled grid is never what that keystroke meant.
  document.addEventListener('keydown', function (ev) {
    if (ev.key !== 'Enter') return;
    var source = ev.target.closest('[data-fill-value]');
    if (!source) return;
    ev.preventDefault();
    var button = source.closest('[data-hours-fill]').querySelector('[data-fill-apply="weekdays"]');
    if (button) fill(button);
  });

  // ---------------------------------------------------------------------
  // Override toggles: a checkbox that governs a block of fields.
  // ---------------------------------------------------------------------

  function syncToggle(checkbox) {
    var body = document.getElementById(checkbox.getAttribute('data-toggle'));
    if (!body) return;
    body.classList.toggle('is-inherited', !checkbox.checked);
    Array.prototype.forEach.call(body.querySelectorAll('input, select, textarea'), function (field) {
      field.disabled = !checkbox.checked;
    });
  }

  function initToggles(root) {
    Array.prototype.forEach.call(root.querySelectorAll('[data-toggle]'), syncToggle);
  }

  document.addEventListener('change', function (ev) {
    var checkbox = ev.target.closest('[data-toggle]');
    if (checkbox) syncToggle(checkbox);
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { initToggles(document); });
  } else {
    initToggles(document);
  }
})();
