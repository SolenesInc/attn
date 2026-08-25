import type { ReactElement, ReactNode } from "react";
/** There is no fourth: a view needing another meaning needs another component. */
export type ButtonVariant = "primary" | "secondary" | "danger";
export interface ButtonProps {
    children?: ReactNode;
    variant?: ButtonVariant;
    disabled?: boolean;
    onClick?: () => void;
    type?: "button" | "submit";
    title?: string;
    className?: string;
}
export declare function Button({ children, variant, disabled, onClick, type, title, className, }: ButtonProps): ReactElement;
export interface TextInputProps {
    value: string;
    onChange: (value: string) => void;
    placeholder?: string;
    disabled?: boolean;
    error?: string;
    ariaLabel?: string;
    className?: string;
}
export declare function TextInput({ value, onChange, placeholder, disabled, error, ariaLabel, className, }: TextInputProps): ReactElement;
export interface TextAreaProps extends TextInputProps {
    /** Visible rows; the box does not grow on its own. */
    rows?: number;
}
export declare function TextArea({ value, onChange, placeholder, disabled, error, ariaLabel, className, rows, }: TextAreaProps): ReactElement;
export interface ListProps {
    children?: ReactNode;
    className?: string;
}
export declare function List({ children, className }: ListProps): ReactElement;
export interface ListRowProps {
    title: ReactNode;
    meta?: ReactNode;
    actions?: ReactNode;
    /** A row that answers a click is focusable and reachable by keyboard. */
    onClick?: () => void;
    selected?: boolean;
    className?: string;
}
export declare function ListRow({ title, meta, actions, onClick, selected, className, }: ListRowProps): ReactElement;
export interface EmptyStateProps {
    title: string;
    hint?: ReactNode;
    className?: string;
}
export declare function EmptyState({ title, hint, className }: EmptyStateProps): ReactElement;
export interface MarkdownProps {
    children: string;
    className?: string;
}
/** Read-only markdown with GitHub tables and task lists. No raw HTML, no scripts. */
export declare function Markdown({ children, className }: MarkdownProps): ReactElement;
