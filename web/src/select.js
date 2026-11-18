import { createEffect } from 'solid-js';

// Moving or replacing the selected option can reset a native select even when its setting stays the same.
export function selectValue(value, options) {
  let select;
  createEffect(
    () => [value(), options()],
    ([selected]) => {
      select.value = selected;
    },
  );
  return el => {
    select = el;
  };
}
