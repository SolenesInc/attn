export type AppActionProps = Record<string, string | number>;
type AppActionSink = (name: string, props: AppActionProps) => void;

let sink: AppActionSink | null = null;

export function setAppActionSink(next: AppActionSink | null): void {
  sink = next;
}

export function recordAppAction(name: string, props: AppActionProps): void {
  sink?.(name, props);
}
