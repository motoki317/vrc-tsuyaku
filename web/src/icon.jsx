export function Icon(props) {
  return (
    <svg class={'icon' + (props.extra ? ' ' + props.extra : '')} aria-hidden="true">
      <use href={'#i-' + props.name} />
    </svg>
  );
}
