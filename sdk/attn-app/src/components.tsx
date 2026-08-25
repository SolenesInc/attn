// Class names only: a CSS import here would emit an asset no page links, so the stylesheet lives in app/src/components/appViews/appSdkComponents.css.
// A spinner and a relative-time label are deliberately absent: both repaint forever in a window open all day.

import type { ChangeEvent, KeyboardEvent, ReactElement, ReactNode } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

function classes(...names: Array<string | false | null | undefined>): string {
  return names.filter(Boolean).join(" ")
}

/** There is no fourth: a view needing another meaning needs another component. */
export type ButtonVariant = "primary" | "secondary" | "danger"

export interface ButtonProps {
  children?: ReactNode
  variant?: ButtonVariant
  disabled?: boolean
  onClick?: () => void
  type?: "button" | "submit"
  title?: string
  className?: string
}

export function Button({
  children,
  variant = "secondary",
  disabled,
  onClick,
  type = "button",
  title,
  className,
}: ButtonProps): ReactElement {
  return (
    <button
      type={type}
      className={classes("attn-app-button", `attn-app-button-${variant}`, className)}
      disabled={disabled}
      onClick={onClick}
      title={title}
    >
      {children}
    </button>
  )
}

export interface TextInputProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  error?: string
  ariaLabel?: string
  className?: string
}

export function TextInput({
  value,
  onChange,
  placeholder,
  disabled,
  error,
  ariaLabel,
  className,
}: TextInputProps): ReactElement {
  return (
    <div className={classes("attn-app-field", className)}>
      <input
        type="text"
        className={classes("attn-app-input", error && "attn-app-input-invalid")}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={ariaLabel}
        aria-invalid={error ? true : undefined}
        onChange={(event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)}
      />
      {error && <div className="attn-app-field-error">{error}</div>}
    </div>
  )
}

export interface TextAreaProps extends TextInputProps {
  /** Visible rows; the box does not grow on its own. */
  rows?: number
}

export function TextArea({
  value,
  onChange,
  placeholder,
  disabled,
  error,
  ariaLabel,
  className,
  rows = 3,
}: TextAreaProps): ReactElement {
  return (
    <div className={classes("attn-app-field", className)}>
      <textarea
        className={classes("attn-app-textarea", error && "attn-app-input-invalid")}
        value={value}
        rows={rows}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={ariaLabel}
        aria-invalid={error ? true : undefined}
        onChange={(event: ChangeEvent<HTMLTextAreaElement>) => onChange(event.target.value)}
      />
      {error && <div className="attn-app-field-error">{error}</div>}
    </div>
  )
}

export interface ListProps {
  children?: ReactNode
  className?: string
}

export function List({ children, className }: ListProps): ReactElement {
  return (
    <div className={classes("attn-app-list", className)} role="list">
      {children}
    </div>
  )
}

export interface ListRowProps {
  title: ReactNode
  meta?: ReactNode
  actions?: ReactNode
  /** A row that answers a click is focusable and reachable by keyboard. */
  onClick?: () => void
  selected?: boolean
  className?: string
}

export function ListRow({
  title,
  meta,
  actions,
  onClick,
  selected,
  className,
}: ListRowProps): ReactElement {
  const interactive = !!onClick
  return (
    <div
      className={classes(
        "attn-app-list-row",
        interactive && "attn-app-list-row-interactive",
        selected && "attn-app-list-row-selected",
        className,
      )}
      role={interactive ? "button" : "listitem"}
      tabIndex={interactive ? 0 : undefined}
      aria-selected={selected}
      onClick={onClick}
      onKeyDown={
        interactive
          ? (event: KeyboardEvent<HTMLDivElement>) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault()
                onClick?.()
              }
            }
          : undefined
      }
    >
      <div className="attn-app-list-row-body">
        <div className="attn-app-list-row-title">{title}</div>
        {meta && <div className="attn-app-list-row-meta">{meta}</div>}
      </div>
      {actions && <div className="attn-app-list-row-actions">{actions}</div>}
    </div>
  )
}

export interface EmptyStateProps {
  title: string
  hint?: ReactNode
  className?: string
}

export function EmptyState({ title, hint, className }: EmptyStateProps): ReactElement {
  return (
    <div className={classes("attn-app-empty", className)}>
      <div className="attn-app-empty-title">{title}</div>
      {hint && <div className="attn-app-empty-hint">{hint}</div>}
    </div>
  )
}

export interface MarkdownProps {
  children: string
  className?: string
}

/** Read-only markdown with GitHub tables and task lists. No raw HTML, no scripts. */
export function Markdown({ children, className }: MarkdownProps): ReactElement {
  return (
    <div className={classes("attn-app-markdown", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  )
}
