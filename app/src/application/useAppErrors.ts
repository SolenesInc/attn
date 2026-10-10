import { useEffect } from 'react';
import { useToast } from '../components/Toast';
import { AppContentProps } from './appSupport';

interface Options {
  settingError: AppContentProps['settingError'];
  clearSettingError: AppContentProps['clearSettingError'];
}
export function useAppErrors({ settingError, clearSettingError }: Options) {
  const { showError } = useToast();
  useEffect(() => {
    if (!settingError) {
      return;
    }
    showError(settingError);
    clearSettingError();
  }, [clearSettingError, settingError, showError]);

  return {
    showError,
  };
}
