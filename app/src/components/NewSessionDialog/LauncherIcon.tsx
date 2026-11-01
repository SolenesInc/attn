const PATHS = {
  recent: (
    <>
      <circle cx="8" cy="8" r="6" />
      <path d="M8 4.8V8l2.2 1.4" />
    </>
  ),
  folder: <path d="M2 4.5h4l1.4 1.6H14v6.4H2z" />,
  repo: (
    <>
      <circle cx="4.5" cy="4" r="1.6" />
      <circle cx="4.5" cy="12" r="1.6" />
      <circle cx="11.5" cy="6" r="1.6" />
      <path d="M4.5 5.6v4.8M11.5 7.6c0 2-2.5 2.2-7 3" />
    </>
  ),
  new: <path d="M8 3v10M3 8h10" />,
  main: (
    <>
      <circle cx="8" cy="8" r="2.6" />
      <path d="M8 2v3.4M8 10.6V14" />
    </>
  ),
  existing: (
    <>
      <circle cx="4.5" cy="4" r="1.6" />
      <circle cx="11.5" cy="12" r="1.6" />
      <path d="M4.5 5.6c0 4 7 2.4 7 4.8" />
    </>
  ),
  machine: (
    <>
      <rect x="2" y="3" width="12" height="8" rx="1.2" />
      <path d="M6 13.5h4M8 11v2.5" />
    </>
  ),
  elsewhere: (
    <>
      <circle cx="7" cy="7" r="4" />
      <path d="M10 10l3.5 3.5" />
    </>
  ),
  delete: <path d="M3 5h10M6 5V3.5h4V5M5 5l.6 8h4.8L11 5" />,
};

export function LauncherIcon({ kind }: { kind: keyof typeof PATHS }) {
  return (
    <span className="launcher-icon" aria-hidden="true">
      <svg viewBox="0 0 16 16">{PATHS[kind]}</svg>
    </span>
  );
}
