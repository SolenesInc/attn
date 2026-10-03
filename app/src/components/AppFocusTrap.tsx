import NativeFocusTrap from 'focus-trap-react';
import { Children, cloneElement, type ComponentProps, type ReactElement, type Ref } from 'react';
import { useToastHost } from '../utils/toastHost';

export default function AppFocusTrap(props: ComponentProps<typeof NativeFocusTrap>) {
  const child = Children.only(props.children) as ReactElement<{ ref?: Ref<HTMLElement> }>;
  const ref = useToastHost(props.active !== false, child.props.ref);
  return <NativeFocusTrap {...props}>{cloneElement(child, { ref })}</NativeFocusTrap>;
}
