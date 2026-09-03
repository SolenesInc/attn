import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { assertProductionRunAllowed, bundleIdentifierForProfile } from './harnessProfile.mjs';
import { appPlatform, createWindowDriver } from './platform.mjs';

const execFileAsync = promisify(execFile);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function runAppleScript(script) {
  const { stdout } = await execFileAsync('osascript', ['-e', script], {
    timeout: 10_000,
  });
  return stdout.trim();
}

async function activateBundle(bundleId) {
  try {
    await runAppleScript(`tell application id "${bundleId}" to activate`);
  } catch {
  }
}

function parseWindowBoundsOutput(output, bundleId) {
  const [x, y, width, height, processName] = String(output || '')
    .split(',')
    .map((value) => value.trim());

  const numericBounds = [x, y, width, height].map((value) => Number.parseInt(value, 10));
  if (!numericBounds.every((value) => Number.isFinite(value))) {
    throw new Error(`Failed to parse window bounds for bundle ${bundleId}: ${output}`);
  }

  return {
    x: numericBounds[0],
    y: numericBounds[1],
    width: numericBounds[2],
    height: numericBounds[3],
    processName: processName || null,
  };
}

function normalizeLogicalBounds(bounds) {
  if (!bounds) {
    return null;
  }
  const x = Number(bounds.x);
  const y = Number(bounds.y);
  const width = Number(bounds.width);
  const height = Number(bounds.height);
  if (![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    return null;
  }
  return {
    x: Math.round(x),
    y: Math.round(y),
    width: Math.round(width),
    height: Math.round(height),
    processName: 'attn-app-window',
  };
}

async function readUiAutomationWindowBounds(client) {
  if (!client || typeof client.request !== 'function') {
    return null;
  }
  try {
    const result = await client.request('get_window_bounds', {}, { timeoutMs: 5_000 });
    if (result?.minimized === true) {
      return null;
    }
    return normalizeLogicalBounds(result?.logicalBounds);
  } catch {
    return null;
  }
}

async function readFrontWindowBoundsForBundle(bundleId) {
  const output = await runAppleScript(`
tell application "System Events"
  set targetProcess to first application process whose bundle identifier is "${bundleId}"
  if (count of windows of targetProcess) is 0 then
    error "No windows found for ${bundleId}"
  end if
  set p to position of front window of targetProcess
  set s to size of front window of targetProcess
  return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text) & "," & (name of targetProcess as text)
end tell
`);
  return parseWindowBoundsOutput(output, bundleId);
}

// A split half under ATTENTION_MIN_WIDTH (480px) plus its 24px restore gutter
// collapses; behind the 240px sidebar that floor is a 1248px window.
export const SPLIT_FRIENDLY_WINDOW = { width: 1440, height: 900 };

export async function widenWindowForSplitPanes(client, target = SPLIT_FRIENDLY_WINDOW) {
  const current = await getFrontWindowBounds(client.bundleId, { client });
  if ((current?.width || 0) >= target.width && (current?.height || 0) >= target.height) {
    return current;
  }
  return setFrontWindowBounds({
    x: 80,
    y: 80,
    width: Math.max(current?.width || 0, target.width),
    height: Math.max(current?.height || 0, target.height),
  }, { client });
}

export async function getFrontWindowBounds(bundleId = null, options = {}) {
  const targetBundleId = bundleId || options.client?.bundleId || bundleIdentifierForProfile();
  assertProductionRunAllowed({ bundleId: targetBundleId });
  const automationBounds = await readUiAutomationWindowBounds(options.client);
  if (automationBounds) {
    return automationBounds;
  }

  if (appPlatform.os === 'linux') {
    const appPath = options.appPath || options.client?.appPath;
    const driver = options.driver || createWindowDriver({ appPath });
    return {
      ...await driver.windowGeometry(),
      processName: 'attn-app-window',
    };
  }

  let lastError = null;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if (attempt > 0) {
      await activateBundle(targetBundleId);
      await delay(250);
    }
    try {
      return await readFrontWindowBoundsForBundle(targetBundleId);
    } catch (error) {
      lastError = error;
    }
  }

  throw lastError instanceof Error
    ? lastError
    : new Error(`Failed to resolve front window bounds for ${targetBundleId}`);
}

function boundsWithinTolerance(actual, target, tolerancePx = 6) {
  if (!actual || !target) {
    return false;
  }
  return (
    Math.abs(actual.x - target.x) <= tolerancePx &&
    Math.abs(actual.y - target.y) <= tolerancePx &&
    Math.abs(actual.width - target.width) <= tolerancePx &&
    Math.abs(actual.height - target.height) <= tolerancePx
  );
}

async function setFrontWindowBoundsForBundle(bundleId, bounds) {
  const output = await runAppleScript(`
tell application "System Events"
  set targetProcess to first application process whose bundle identifier is "${bundleId}"
  if (count of windows of targetProcess) is 0 then
    error "No windows found for ${bundleId}"
  end if
  set position of front window of targetProcess to {${bounds.x}, ${bounds.y}}
  set size of front window of targetProcess to {${bounds.width}, ${bounds.height}}
  set p to position of front window of targetProcess
  set s to size of front window of targetProcess
  return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text) & "," & (name of targetProcess as text)
end tell
`);
  return parseWindowBoundsOutput(output, bundleId);
}

async function writeUiAutomationWindowBounds(client, targetBounds) {
  if (!client || typeof client.request !== 'function') {
    return null;
  }
  try {
    const result = await client.request(
      'set_window_bounds',
      { logicalBounds: targetBounds },
      { timeoutMs: 5_000 },
    );
    return normalizeLogicalBounds(result?.logicalBounds);
  } catch {
    return null;
  }
}

export async function setFrontWindowBounds(targetBounds, options = {}) {
  const bundleId = options.bundleId || options.client?.bundleId || bundleIdentifierForProfile();
  assertProductionRunAllowed({ bundleId });
  const normalizedTarget = normalizeLogicalBounds(targetBounds);
  if (!normalizedTarget) {
    throw new Error(`Invalid target window bounds: ${JSON.stringify(targetBounds)}`);
  }

  const automationResult = await writeUiAutomationWindowBounds(options.client, normalizedTarget);
  if (
    automationResult &&
    boundsWithinTolerance(automationResult, normalizedTarget, options.settleTolerancePx ?? 8)
  ) {
    return automationResult;
  }

  if (appPlatform.os === 'linux') {
    const appPath = options.appPath || options.client?.appPath;
    const driver = options.driver || createWindowDriver({ appPath });
    return {
      ...await driver.setWindowBounds(normalizedTarget),
      processName: 'attn-app-window',
    };
  }

  let lastError = null;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    if (attempt > 0) {
      await delay(250);
    }
    await activateBundle(bundleId);
    try {
      const result = await setFrontWindowBoundsForBundle(bundleId, normalizedTarget);
      if (boundsWithinTolerance(result, normalizedTarget, options.settleTolerancePx ?? 8)) {
        return result;
      }
      break;
    } catch (error) {
      lastError = error;
    }
  }

  if (lastError) {
    throw lastError;
  }

  const settleTimeoutMs = options.settleTimeoutMs ?? 6_000;
  const retryIntervalMs = options.retryIntervalMs ?? 150;
  const settleTolerancePx = options.settleTolerancePx ?? 8;
  const startedAt = Date.now();
  let lastBounds = null;

  while (Date.now() - startedAt < settleTimeoutMs) {
    lastBounds = await getFrontWindowBounds(bundleId, options);
    if (boundsWithinTolerance(lastBounds, normalizedTarget, settleTolerancePx)) {
      return lastBounds;
    }
    await delay(retryIntervalMs);
  }

  throw new Error(
    `Timed out waiting for ${bundleId} window bounds to settle at ${JSON.stringify(normalizedTarget)}. ` +
    `Last bounds: ${JSON.stringify(lastBounds)}`
  );
}

// `crop` coordinates are relative to the window's top-left corner.
export function resolveCaptureRect(windowBounds, crop = null) {
  if (!windowBounds) {
    throw new Error('resolveCaptureRect requires windowBounds');
  }
  if (!crop) {
    return {
      x: windowBounds.x,
      y: windowBounds.y,
      width: windowBounds.width,
      height: windowBounds.height,
    };
  }

  const cropX = Number(crop.x);
  const cropY = Number(crop.y);
  const cropWidth = Number(crop.width);
  const cropHeight = Number(crop.height);
  if (![cropX, cropY, cropWidth, cropHeight].every(Number.isFinite) || cropWidth <= 0 || cropHeight <= 0) {
    throw new Error(`Invalid crop rect: ${JSON.stringify(crop)}`);
  }

  const windowRight = windowBounds.width;
  const windowBottom = windowBounds.height;
  const cropRight = cropX + cropWidth;
  const cropBottom = cropY + cropHeight;

  const clampedLeft = Math.max(0, Math.min(cropX, windowRight));
  const clampedTop = Math.max(0, Math.min(cropY, windowBottom));
  const clampedRight = Math.max(0, Math.min(cropRight, windowRight));
  const clampedBottom = Math.max(0, Math.min(cropBottom, windowBottom));

  const clampedWidth = clampedRight - clampedLeft;
  const clampedHeight = clampedBottom - clampedTop;

  if (clampedWidth <= 0 || clampedHeight <= 0) {
    throw new Error(
      `Crop rect ${JSON.stringify(crop)} does not overlap window bounds ${JSON.stringify(windowBounds)}`,
    );
  }

  return {
    x: windowBounds.x + clampedLeft,
    y: windowBounds.y + clampedTop,
    width: clampedWidth,
    height: clampedHeight,
  };
}

export function parseSipsPixelDimensions(stdout) {
  const text = String(stdout || '');
  const widthMatch = /pixelWidth:\s*(\d+)/.exec(text);
  const heightMatch = /pixelHeight:\s*(\d+)/.exec(text);
  if (!widthMatch || !heightMatch) {
    throw new Error(`Failed to parse sips pixel dimensions from output: ${stdout}`);
  }
  return {
    width: Number.parseInt(widthMatch[1], 10),
    height: Number.parseInt(heightMatch[1], 10),
  };
}

export function parseIdentifyPixelDimensions(stdout) {
  const [width, height] = String(stdout || '').trim().split(/\s+/).map(Number);
  if (![width, height].every(Number.isInteger) || width <= 0 || height <= 0) {
    throw new Error(`Failed to parse ImageMagick pixel dimensions from output: ${stdout}`);
  }
  return { width, height };
}

async function readPngPixelDimensions(filePath) {
  if (appPlatform.os === 'linux') {
    const { stdout } = await execFileAsync(
      'identify',
      ['-format', '%w %h', filePath],
      { timeout: 10_000 },
    );
    return parseIdentifyPixelDimensions(stdout);
  }
  const { stdout } = await execFileAsync(
    '/usr/bin/sips',
    ['-g', 'pixelWidth', '-g', 'pixelHeight', filePath],
    { timeout: 10_000 },
  );
  return parseSipsPixelDimensions(stdout);
}

const SCREENSHOT_CAP_HIT = /capture_screenshot_data never returned a frame|Automation request timed out: capture_screenshot_data/;

export function isScreenshotCapHit(error) {
  return SCREENSHOT_CAP_HIT.test(error?.message || '');
}

export function describeScreenshotTimeout(error, { outputPath, selector }) {
  if (!/Automation request timed out/.test(error?.message || '')) {
    return error;
  }
  return new Error(
    `${error.message}: capture_screenshot_data never returned a frame for ${selector || 'the whole window'} `
    + `(${outputPath}). WKWebView parks requestAnimationFrame when the window is occluded, which is what the `
    + 'harness launches always-on-top to avoid; a scenario that opted out of always-on-top must keep its window '
    + 'in front of whatever is covering it.',
  );
}

export async function captureScreenshotData(outputPath, { client, selector, timeoutMs } = {}) {
  if (!client || typeof client.request !== 'function') {
    throw new Error('captureScreenshotData requires a UI automation client');
  }
  const result = await client
    .request('capture_screenshot_data', selector ? { selector } : {}, timeoutMs ? { timeoutMs } : {})
    .catch((error) => {
      throw describeScreenshotTimeout(error, { outputPath, selector });
    });
  if (typeof result?.pngBase64 !== 'string' || result.pngBase64.length === 0) {
    throw new Error('capture_screenshot_data returned no PNG data');
  }
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  fs.writeFileSync(outputPath, Buffer.from(result.pngBase64, 'base64'));
  return {
    source: 'dom',
    path: outputPath,
    ...(selector ? { selector } : {}),
  };
}

export async function captureEvidenceScreenshot(outputPath, options = {}) {
  try {
    return await captureScreenshotData(outputPath, options);
  } catch (error) {
    if (isScreenshotCapHit(error)) {
      throw error;
    }
    const log = options.log || ((message) => console.warn(message));
    log(`[RealAppHarness] evidence screenshot missing: ${outputPath}: ${error?.message || error}`);
    return null;
  }
}

export async function captureFrontWindowScreenshot(outputPath, options = {}) {
  const bundleId = options.bundleId || options.client?.bundleId || bundleIdentifierForProfile();
  assertProductionRunAllowed({ bundleId });
  const bounds = await getFrontWindowBounds(bundleId, options);
  const captureRect = resolveCaptureRect(bounds, options.crop || null);
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });

  if (appPlatform.os === 'linux') {
    const appPath = options.appPath || options.client?.appPath;
    const driver = options.driver || createWindowDriver({ appPath });
    const needsTransform = Boolean(options.crop) || (options.maxDim !== undefined && options.maxDim !== null);
    const capturedPath = needsTransform ? `${outputPath}.full.png` : outputPath;
    await driver.screenshot(capturedPath);

    if (options.maxDim !== undefined && options.maxDim !== null) {
      const maxDim = options.maxDim;
      if (!Number.isInteger(maxDim) || maxDim <= 0) {
        throw new Error(`Invalid maxDim: ${JSON.stringify(options.maxDim)}`);
      }
    }

    if (needsTransform) {
      const args = [capturedPath];
      if (options.crop) {
        const cropX = captureRect.x - bounds.x;
        const cropY = captureRect.y - bounds.y;
        args.push('-crop', `${captureRect.width}x${captureRect.height}+${cropX}+${cropY}`, '+repage');
      }
      if (options.maxDim !== undefined && options.maxDim !== null) {
        args.push('-resize', `${options.maxDim}x${options.maxDim}>`);
      }
      args.push(outputPath);
      try {
        await execFileAsync('convert', args, { timeout: 10_000 });
      } finally {
        fs.rmSync(capturedPath, { force: true });
      }
    }

    const pixelDimensions = await readPngPixelDimensions(outputPath);
    return {
      source: 'native_window',
      bundleId,
      bounds,
      captureRect,
      pixelDimensions,
      path: outputPath,
    };
  }

  await execFileAsync('/usr/sbin/screencapture', [
    '-x',
    '-R',
    `${captureRect.x},${captureRect.y},${captureRect.width},${captureRect.height}`,
    outputPath,
  ], {
    timeout: 10_000,
  });

  let pixelDimensions = null;
  if (options.maxDim !== undefined && options.maxDim !== null) {
    const maxDim = options.maxDim;
    if (!Number.isInteger(maxDim) || maxDim <= 0) {
      throw new Error(`Invalid maxDim: ${JSON.stringify(options.maxDim)}`);
    }
    // captureRect is in logical points; on Retina displays screencapture
    // writes the PNG at 2x pixels, so gate on the file's actual dimensions.
    pixelDimensions = await readPngPixelDimensions(outputPath);
    if (Math.max(pixelDimensions.width, pixelDimensions.height) > maxDim) {
      await execFileAsync('/usr/bin/sips', ['-Z', String(maxDim), outputPath], {
        timeout: 10_000,
      });
      pixelDimensions = await readPngPixelDimensions(outputPath);
    }
  }

  return {
    source: 'native_window',
    bundleId,
    bounds,
    captureRect,
    ...(pixelDimensions ? { pixelDimensions } : {}),
    path: outputPath,
  };
}
