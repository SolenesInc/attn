import type { AskAnswer, AskQuestion } from '../../types/generated';
import { MarkdownReader } from '../MarkdownReader';
import type { MarkdownDocumentSource } from '../MarkdownReader/documentSource';

interface AskQuestionCardProps {
  askId: string;
  index: number;
  question: AskQuestion;
  answer: AskAnswer | undefined;
  source: MarkdownDocumentSource;
  onChange: (answer: AskAnswer) => void;
}

export function AskQuestionCard({ askId, index, question, answer, source, onChange }: AskQuestionCardProps) {
  const picked = answer?.picked ?? [];
  const other = answer?.other ?? '';
  const text = answer?.text ?? '';
  const name = `${askId}-${question.key}`;
  const update = (next: Partial<AskAnswer>) => onChange({ key: question.key, picked, other, text, ...next });
  const pick = (label: string) => {
    if (question.kind === 'one') {
      update({ picked: [label], other: '' });
      return;
    }
    update({ picked: picked.includes(label) ? picked.filter((value) => value !== label) : [...picked, label] });
  };
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
      {question.kind === 'text' ? (
        <div className="ask-written">
          <textarea
            className="ask-written-input"
            aria-label={question.prompt}
            value={text}
            rows={3}
            placeholder={question.recommended ? `Suggested: ${question.recommended}` : 'Your answer…'}
            onChange={(event) => update({ text: event.target.value })}
          />
          {question.recommended && text.trim() === '' && (
            <button type="button" className="ask-use-suggestion" onClick={() => update({ text: question.recommended })}>
              Use the suggestion
            </button>
          )}
        </div>
      ) : (
        <div className="ask-choices" role={question.kind === 'one' ? 'radiogroup' : 'group'} aria-label={question.prompt}>
          {question.choices.map((choice) => (
            <label key={choice.label} className={`ask-choice ${picked.includes(choice.label) ? 'ask-choice--picked' : ''}`.trim()}>
              <input
                type={question.kind === 'one' ? 'radio' : 'checkbox'}
                name={name}
                checked={picked.includes(choice.label)}
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
          <input
            className="ask-other"
            type="text"
            aria-label={`Other answer to ${question.prompt}`}
            placeholder="Other…"
            value={other}
            onChange={(event) => update(question.kind === 'one' ? { other: event.target.value, picked: [] } : { other: event.target.value })}
          />
        </div>
      )}
    </fieldset>
  );
}
