import type { AskAnswer, AskQuestion } from '../../types/generated';
import { MarkdownReader } from '../MarkdownReader';
import type { MarkdownDocumentSource } from '../MarkdownReader/documentSource';

interface AskQuestionCardProps {
  askId: string;
  index: number;
  question: AskQuestion;
  answer: AskAnswer | undefined;
  source: MarkdownDocumentSource;
  onChange?: (answer: AskAnswer) => void;
}

interface AnswerProps {
  question: AskQuestion;
  answer: AskAnswer | undefined;
  onChange?: (answer: AskAnswer) => void;
}

export function AskQuestionCard({ askId, index, question, answer, source, onChange }: AskQuestionCardProps) {
  return (
    <fieldset className="ask-question" data-testid={`ask-question-${question.key}`}>
      <legend className="ask-question-prompt">
        <span className="ask-question-index">{index}</span>
        {question.prompt}
      </legend>
      {question.context && (
        <div className="asks-markdown ask-question-context">
          <MarkdownReader content={question.context} source={source} />
        </div>
      )}
      {question.kind === 'text'
        ? <WrittenAnswer question={question} answer={answer} onChange={onChange} />
        : <ChoiceAnswer name={`${askId}-${question.key}`} question={question} answer={answer} onChange={onChange} />}
    </fieldset>
  );
}

function changedAnswer(question: AskQuestion, answer: AskAnswer | undefined, next: Partial<AskAnswer>): AskAnswer {
  return { key: question.key, picked: answer?.picked ?? [], other: answer?.other ?? '', text: answer?.text ?? '', ...next };
}

function WrittenAnswer({ question, answer, onChange }: AnswerProps) {
  const text = answer?.text ?? '';
  if (!onChange) return <RepliedText text={text} label={question.prompt} />;
  const write = (value: string) => onChange(changedAnswer(question, answer, { text: value }));
  return (
    <div className="ask-written">
      <textarea
        className="ask-written-input"
        aria-label={question.prompt}
        value={text}
        rows={3}
        placeholder={question.recommended ? `Suggested: ${question.recommended}` : 'Your answer…'}
        onChange={(event) => write(event.target.value)}
      />
      {question.recommended && text.trim() === '' && (
        <button type="button" className="ask-use-suggestion" onClick={() => write(question.recommended ?? '')}>
          Use the suggestion
        </button>
      )}
    </div>
  );
}

function ChoiceAnswer({ name, question, answer, onChange }: AnswerProps & { name: string }) {
  const picked = answer?.picked ?? [];
  const pickedLabels = new Set(picked);
  const other = answer?.other ?? '';
  const one = question.kind === 'one';
  const update = (next: Partial<AskAnswer>) => onChange?.(changedAnswer(question, answer, next));
  const pick = (label: string) => update(one
    ? { picked: [label], other: '' }
    : { picked: pickedLabels.has(label) ? picked.filter((value) => value !== label) : [...picked, label] });
  return (
    <div className="ask-choices" role={one ? 'radiogroup' : 'group'} aria-label={question.prompt}>
      {question.choices.map((choice) => (
        <label key={choice.label} className={`ask-choice ${pickedLabels.has(choice.label) ? 'ask-choice--picked' : ''}`.trim()}>
          <input
            type={one ? 'radio' : 'checkbox'}
            name={name}
            checked={pickedLabels.has(choice.label)}
            disabled={!onChange}
            onChange={() => pick(choice.label)}
          />
          <span className="ask-choice-text">
            <span className="ask-choice-label">
              {choice.label}
              {question.recommended === choice.label && <span className="ask-recommended">recommended</span>}
            </span>
            {choice.description && <span className="ask-choice-description">{choice.description}</span>}
          </span>
        </label>
      ))}
      <OtherAnswer label={`Other answer to ${question.prompt}`} value={other} onChange={onChange && ((value) => update(one ? { other: value, picked: [] } : { other: value }))} />
    </div>
  );
}

function OtherAnswer({ label, value, onChange }: { label: string; value: string; onChange?: (value: string) => void }) {
  if (!onChange) return value ? <RepliedText text={value} label={label} /> : null;
  return (
    <input
      className="ask-other"
      type="text"
      aria-label={label}
      placeholder="Other…"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function RepliedText({ text, label }: { text: string; label: string }) {
  return (
    <p className={`ask-replied-text ${text ? '' : 'ask-replied-text--none'}`.trim()} aria-label={label}>
      {text || 'Not answered'}
    </p>
  );
}
