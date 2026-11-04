import type { SaveSetting } from './SettingsAutosave';

const OWN_PR_COMMENTS_SETTING = 'pull_requests.own_comments';
const TENDER_WAKE_SETTING = 'garden.tender_wake';

type TenderWake = 'any' | 'watches' | 'off';

const TENDER_WAKE_CHOICES: Array<{ value: TenderWake; label: string }> = [
  { value: 'any', label: 'Any mail' },
  { value: 'watches', label: 'PRs and reminders' },
  { value: 'off', label: 'Never' },
];

const STANDBY_MAIL: Array<{ label: string; wakes: TenderWake[] }> = [
  { label: 'PR #123 got approved', wakes: ['any', 'watches'] },
  { label: 'Its reminder came up', wakes: ['any', 'watches'] },
  { label: 'Another agent messaged the seed', wakes: ['any'] },
  { label: 'You answered its ask', wakes: ['any'] },
];

function tenderWakeChoice(value: string | undefined): TenderWake {
  return value === 'watches' || value === 'off' ? value : 'any';
}

interface SeedsOnStandbySettingsProps {
  settings: Record<string, string>;
  onSetSetting: SaveSetting;
}

export function SeedsOnStandbySettings({ settings, onSetSetting }: SeedsOnStandbySettingsProps) {
  const tenderWake = tenderWakeChoice(settings[TENDER_WAKE_SETTING]);
  const ringOnOwnPullRequestComments = settings[OWN_PR_COMMENTS_SETTING] === 'ring';
  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <h3>Seeds on standby</h3>
        <p className="settings-description">Wake a closed agent when mail reaches its seed.</p>
      </div>
      <div className="settings-block-body">
        <div className="settings-segmented" role="group" aria-label="Wake for">
          {TENDER_WAKE_CHOICES.map(({ value, label }) => (
            <button
              key={value}
              type="button"
              className={`settings-segmented-option ${tenderWake === value ? 'active' : ''}`}
              aria-pressed={tenderWake === value}
              onClick={() => onSetSetting(TENDER_WAKE_SETTING, value)}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="settings-wake-explain" aria-live="polite">
          <p className="settings-wake-explain-title">A closed agent on standby</p>
          <ul className="settings-wake-cases">
            {STANDBY_MAIL.map(({ label, wakes }) => {
              const wakesNow = wakes.includes(tenderWake);
              return (
                <li key={label}>
                  <span className={`settings-wake-verdict ${wakesNow ? 'wakes' : 'waits'}`}>
                    {wakesNow ? 'Wakes' : 'Waits'}
                  </span>
                  <span>{label}</span>
                </li>
              );
            })}
          </ul>
          <p className="settings-hint">A crew member who tends the seed always wakes, whatever you pick.</p>
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
