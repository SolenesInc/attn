import { Component, type ReactNode } from 'react';

export class MarkdownBoundary extends Component<
  { children: ReactNode; fallback: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidUpdate(previous: { children: ReactNode }) {
    // The next delta is a different document. A message that failed half-typed
    // is usually renderable once it is whole, so the boundary re-arms.
    if (this.state.failed && previous.children !== this.props.children) this.setState({ failed: false });
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}
