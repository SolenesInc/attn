import { useSettings } from '../contexts/SettingsContext';
import { COLOR_OVERRIDES_SETTING, COLOR_SCHEME_SETTING, hasColorOverrides } from './resolve';
import { DEFAULT_SCHEME_ID, SCHEMES } from './schemes';
import { useAppearance } from './useColors';

const SWATCHES = ['bg_app', 'bg_panel', 'text_primary', 'accent', 'brand', 'state_working', 'state_waiting'] as const;

export function ColorSchemeSettings() {
  const { settings, setSetting } = useSettings();
  const { appearance } = useAppearance(settings);
  const selected = settings[COLOR_SCHEME_SETTING] || DEFAULT_SCHEME_ID;

  return (
    <div className="settings-color-schemes">
      <div className="settings-color-scheme-list" role="group" aria-label="Color scheme">
        {SCHEMES.map(({ id, scheme }) => (
          <button
            key={id}
            type="button"
            className={`settings-color-scheme ${id === selected ? 'active' : ''}`}
            aria-pressed={id === selected}
            onClick={() => setSetting(COLOR_SCHEME_SETTING, id)}
          >
            <span className="settings-color-scheme-swatches" aria-hidden="true">
              {SWATCHES.map((name) => (
                <span key={name} className="settings-color-scheme-swatch" style={{ background: scheme[appearance].ui[name] }} />
              ))}
            </span>
            <span className="settings-color-scheme-name">{scheme.name}</span>
          </button>
        ))}
      </div>
      {hasColorOverrides(settings) && (
        <div className="settings-color-overrides">
          <span className="settings-color-overrides-mark">Custom colors</span>
          <button type="button" className="settings-action" onClick={() => setSetting(COLOR_OVERRIDES_SETTING, '')}>
            Reset
          </button>
        </div>
      )}
    </div>
  );
}
