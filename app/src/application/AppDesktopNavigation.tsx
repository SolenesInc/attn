import { DesktopOverview } from '../components/DesktopOverview';
import { ProfileSwitcher } from '../components/ProfileSwitcher';
import { BoolSetting, isOn } from '../utils/settings';
import { useAppInputs, useDesktopNavigationContext, useNavigationContext } from './AppContexts';

export function AppDesktopNavigation() {
  const { desktopNavigation, desktopOverviewOpen, setDesktopOverviewOpen, profileSwitcherOpen, setProfileSwitcherOpen } =
    useDesktopNavigationContext();
  const { setView } = useNavigationContext();
  const { settings } = useAppInputs();
  const {
    profiles,
    selectedProfile,
    desktops,
    currentDesktop,
    switchToDesktop,
    moveActiveLeafToDesktop,
    createDesktop,
    selectProfile,
    createProfile,
    renameProfile,
    deleteProfile,
  } = desktopNavigation;
  return (
    <>
      {desktopOverviewOpen && (
        <DesktopOverview
          profileName={selectedProfile?.name ?? ''}
          desktops={desktops}
          currentDesktopId={currentDesktop?.id ?? null}
          canSendActivePane={Boolean(currentDesktop?.active_pane_id)}
          onSwitch={(desktopId) => {
            setView('session');
            switchToDesktop(desktopId);
          }}
          onSendActivePane={(desktopId) => moveActiveLeafToDesktop(desktopId, false)}
          onCreate={createDesktop}
          onClose={() => setDesktopOverviewOpen(false)}
        />
      )}
      {profileSwitcherOpen && (
        <ProfileSwitcher
          underTitleBarSelector={isOn(settings, BoolSetting.shelfLayoutEnabled)}
          profiles={profiles}
          selectedProfileId={selectedProfile?.id ?? null}
          onSelect={(profileId) => {
            setView('session');
            selectProfile(profileId);
          }}
          onCreate={async (name) => {
            await createProfile(name);
            setView('session');
          }}
          onRename={renameProfile}
          onDelete={deleteProfile}
          onClose={() => setProfileSwitcherOpen(false)}
        />
      )}
    </>
  );
}
