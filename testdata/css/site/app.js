document.getElementById('app').classList.add('is-ready');
const name = 'fade-in';
element.style.animationName = name;

// Template strings: classes may be glued to ${} or live in ternary quotes.
export function Card({ open, focused }) {
  return (
    <div
      className={`from-template sl-desk-folder ${tone}${open ? ' is-open' : ''}`}
    >
      <span className={`glued-cell${focused ? ' is-focused' : ''}`} />
    </div>
  )
}
