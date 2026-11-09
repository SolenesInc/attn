import type { SaveSetting } from './SettingsAutosave';

const OWN_PR_COMMENTS_SETTING = 'pull_requests.own_comments';
const TENDER_RESUME_SETTING = 'garden.tender_resume';

type TenderResume = 'any' | 'watches' | 'off';

const TENDER_RESUME_CHOICES: Array<{ value: TenderResume; label: string }> = [
  { value: 'any', label: 'Any mail' },
  { value: 'watches', label: 'PRs and reminders' },
  { value: 'off', label: 'Never' },
];

const STANDBY_MAIL: Array<{ label: string; resumes: TenderResume[] }> = [
  { label: 'PR #123 got approved', resumes: ['any', 'watches'] },
  { label: 'Its reminder came up', resumes: ['any', 'watches'] },
  { label: 'Another agent messaged the seed', resumes: ['any'] },
  { label: 'You answered its ask', resumes: ['any'] },
];

function tenderResumeChoice(value: string | undefined): TenderResume {
  return value === 'watches' || value === 'off' ? value : 'any';
}

interface SeedsOnStandbySettingsProps {
  settings: Record<string, string>;
  onSetSetting: SaveSetting;
}

export function SeedsOnStandbySettings({ settings, onSetSetting }: SeedsOnStandbySettingsProps) {
  const tenderResume = tenderResumeChoice(settings[TENDER_RESUME_SETTING]);
  const ringOnOwnPullRequestComments = settings[OWN_PR_COMMENTS_SETTING] === 'ring';
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <h3>Seeds on standby</h3>
        <p className="settings-description">Resume a closed agent when mail reaches its seed.</p>
      </div>
      <div className="settings-block-body">
        <div className="settings-segmented" role="group" aria-label="Resume for">
          {TENDER_RESUME_CHOICES.map(({ value, label }) => (
            <button
              key={value}
              type="button"
              className={`settings-segmented-option ${tenderResume === value ? 'active' : ''}`}
              aria-pressed={tenderResume === value}
              onClick={() => onSetSetting(TENDER_RESUME_SETTING, value)}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="settings-standby-explain" aria-live="polite">
          <p className="settings-standby-explain-title">A closed agent on standby</p>
          <ul className="settings-standby-cases">
            {STANDBY_MAIL.map(({ label, resumes }) => {
              const resumesNow = resumes.includes(tenderResume);
              return (
                <li key={label}>
                  <span className={`settings-standby-verdict ${resumesNow ? 'resumes' : 'waits'}`}>
                    {resumesNow ? 'Resumes' : 'Waits'}
                  </span>
                  <span>{label}</span>
                </li>
              );
            })}
          </ul>
          <p className="settings-hint">attn always starts a session for a crew member who tends the seed, whatever you pick.</p>
        </div>
        <div className="settings-row-card">
          <div>
            <p className="settings-row-title">Ring on my own PR comments</p>
            <p className="settings-row-copy" id="settings-own-pr-comments-description">
              Enable if you want to give feedback to agents by writing comments on the PR
            </p>
          </div>
          <button
            type="button"
            role="switch"
            aria-label="Ring on my own PR comments"
            aria-checked={ringOnOwnPullRequestComments}
            aria-describedby="settings-own-pr-comments-description"
            className="settings-action"
            data-testid="settings-own-pr-comments-toggle"
            onClick={() => onSetSetting(OWN_PR_COMMENTS_SETTING, ringOnOwnPullRequestComments ? 'quiet' : 'ring')}
          >
            {ringOnOwnPullRequestComments ? 'Disable' : 'Enable'}
          </button>
        </div>
      </div>
    </section>
  );
}
