/**
 * Documentation Screenshot Capture
 *
 * Automated Playwright script that navigates through the full app flow and
 * captures screenshots for the README and docs. Run against a live instance:
 *
 *   cd frontend && npx playwright test e2e/capture-screenshots.spec.ts
 *
 * Prerequisites:
 *   - Full system running (./start-all.sh)
 *   - Camera connected and producing frames
 *   - Tailscale active (or TAILSCALE_DEV_MODE=true)
 *
 * Re-run any time the UI changes to refresh all doc images.
 */

import { test, Page } from '@playwright/test';
import * as path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Output to docs/screenshots/ at project root for README embedding
const screenshotDir = path.resolve(__dirname, '..', '..', 'docs', 'screenshots');

// Use the proxy port that start-all.sh provides (Node proxy -> Go API -> frontend)
const BASE_URL = process.env.SCREENSHOT_URL || 'http://localhost:3000';

// Viewport for consistent screenshots
const VIEWPORT = { width: 1280, height: 800 };

// ---- helpers ----

async function screenshot(page: Page, name: string) {
  await page.screenshot({
    path: path.join(screenshotDir, `${name}.png`),
    fullPage: false,
  });
}

async function clickTab(page: Page, label: string) {
  // Desktop header tabs use role="tab"
  await page.locator('header button[role="tab"]').filter({ hasText: label }).click();
  // Let animations and data loads settle
  await page.waitForTimeout(600);
}

async function selectByLabel(page: Page, labelText: string, value: string) {
  // Find the <select> element by its associated label text.
  // Our Select primitive renders: <label for={id}>Text</label> ... <select id={id}>
  const label = page.locator('label').filter({ hasText: labelText });
  const selectId = await label.getAttribute('for');
  if (selectId) {
    await page.locator(`select[id="${selectId}"]`).selectOption(value);
  } else {
    // Fallback: find select near the label
    await label.locator('..').locator('select').first().selectOption(value);
  }
  await page.waitForTimeout(300);
}

// ---- tests ----

test.describe('Documentation Screenshots', () => {
  test.use({
    baseURL: BASE_URL,
    viewport: VIEWPORT,
  });

  test.beforeAll(async () => {
    // Ensure output directory exists
    const fs = await import('fs');
    fs.mkdirSync(screenshotDir, { recursive: true });
  });

  test('capture full app flow', async ({ page }) => {
    // Generous timeout — we're waiting on real backend operations
    test.setTimeout(180_000);

    // ----------------------------------------------------------------
    // 1. CONFIG PAGE — motion-triggered mode
    // ----------------------------------------------------------------
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    // Wait for devices to load (cameras/microphones API calls)
    await page.waitForTimeout(2000);

    // Set recording mode to "Motion-triggered"
    await selectByLabel(page, 'Recording Mode', 'motion');
    await page.waitForTimeout(400);

    // Fill Tailscale fields if empty (validation requires them)
    const nodeNameInput = page.locator('input').filter({ hasText: /node name/i })
      .or(page.getByLabel('Node Name'));
    const currentValue = await nodeNameInput.inputValue().catch(() => '');
    if (!currentValue.trim()) {
      await nodeNameInput.fill('webcam-node');
      await page.getByLabel('Hostname').fill('security-cam');
    }

    await screenshot(page, '01-config-motion');

    // ----------------------------------------------------------------
    // 2. SAVE CONFIG — triggers transition to calibration
    // ----------------------------------------------------------------
    const startButton = page.getByRole('button', { name: /start camera/i });
    await startButton.click();

    // Wait for save to complete and tab to switch
    await page.waitForTimeout(2000);

    // If we auto-navigated to calibration, great. Otherwise click the tab.
    const currentContent = await page.locator('h1, h2').first().textContent().catch(() => '');
    if (!currentContent?.toLowerCase().includes('calibration')) {
      await clickTab(page, 'Calibrate');
    }

    // ----------------------------------------------------------------
    // 3. CALIBRATION PAGE — idle state
    // ----------------------------------------------------------------
    await page.waitForTimeout(500);
    await screenshot(page, '02-calibration-idle');

    // ----------------------------------------------------------------
    // 4. START CALIBRATION
    // ----------------------------------------------------------------
    const startCalibBtn = page.getByRole('button', { name: /start calibration/i });
    if (await startCalibBtn.isVisible()) {
      await startCalibBtn.click();

      // Wait for "recording" state — progress bar should appear
      await page.waitForTimeout(3000);
      await screenshot(page, '03-calibration-recording');

      // Wait for calibration to complete (10s recording + processing)
      // Poll until we see "Apply Calibration" or timeout
      await page.getByRole('button', { name: /apply calibration/i })
        .waitFor({ state: 'visible', timeout: 30_000 })
        .catch(() => { /* may already be complete */ });

      await screenshot(page, '04-calibration-complete');

      // Apply calibration
      const applyBtn = page.getByRole('button', { name: /apply calibration/i });
      if (await applyBtn.isVisible()) {
        await applyBtn.click();
        await page.waitForTimeout(1500);
      }
    }

    // ----------------------------------------------------------------
    // 5. CAMERA PAGE — idle (before connecting)
    // ----------------------------------------------------------------
    // We should have auto-navigated to camera after applying calibration
    const cameraHeadingVisible = await page.locator('text=Connect to Camera')
      .isVisible().catch(() => false);
    if (!cameraHeadingVisible) {
      await clickTab(page, 'Camera');
    }
    await page.waitForTimeout(500);
    await screenshot(page, '05-camera-idle');

    // ----------------------------------------------------------------
    // 6. CONNECT TO CAMERA — live feed
    // ----------------------------------------------------------------
    const connectBtn = page.getByRole('button', { name: /connect to camera/i });
    if (await connectBtn.isVisible()) {
      await connectBtn.click();

      // Wait for the "Live" indicator to appear (WebRTC connected, track attached)
      await page.locator('text=Live').waitFor({ state: 'visible', timeout: 20_000 })
        .catch(() => { /* connection may be slower */ });

      // Give the video a moment to render actual frames
      await page.waitForTimeout(3000);
      await screenshot(page, '06-camera-live');
    }

    // ----------------------------------------------------------------
    // 7. START RECORDING — on camera page (motion mode still has manual button)
    // ----------------------------------------------------------------
    const recordBtn = page.getByRole('button', { name: /^Record$/i });
    if (await recordBtn.isVisible()) {
      await recordBtn.click();
      await page.waitForTimeout(2000);

      // Should see "Recording" indicator and "Rec" badge on video
      await screenshot(page, '07-camera-recording');

      // Let it record a few segments
      await page.waitForTimeout(8000);

      // Stop recording
      const stopBtn = page.getByRole('button', { name: /stop/i });
      if (await stopBtn.isVisible()) {
        await stopBtn.click();
        await page.waitForTimeout(1000);
      }
    }

    // ----------------------------------------------------------------
    // 8. DISCONNECT from camera
    // ----------------------------------------------------------------
    const disconnectBtn = page.getByRole('button', { name: /disconnect/i });
    if (await disconnectBtn.isVisible()) {
      await disconnectBtn.click();
      await page.waitForTimeout(500);
    }

    // ----------------------------------------------------------------
    // 9. SWITCH TO MANUAL MODE — back to config
    // ----------------------------------------------------------------
    await clickTab(page, 'Config');
    await page.waitForTimeout(1000);

    await selectByLabel(page, 'Recording Mode', 'manual');
    await page.waitForTimeout(400);
    await screenshot(page, '08-config-manual');

    // Save to apply manual mode
    const saveBtn = page.getByRole('button', { name: /start camera/i });
    if (await saveBtn.isVisible()) {
      await saveBtn.click();
      await page.waitForTimeout(2000);
    }

    // ----------------------------------------------------------------
    // 10. CAMERA PAGE in manual mode — connect and record
    // ----------------------------------------------------------------
    await clickTab(page, 'Camera');
    await page.waitForTimeout(500);

    const connectBtn2 = page.getByRole('button', { name: /connect to camera/i });
    if (await connectBtn2.isVisible()) {
      await connectBtn2.click();
      await page.locator('text=Live').waitFor({ state: 'visible', timeout: 20_000 })
        .catch(() => {});
      await page.waitForTimeout(3000);
    }

    // Start manual recording
    const recordBtn2 = page.getByRole('button', { name: /^Record$/i });
    if (await recordBtn2.isVisible()) {
      await recordBtn2.click();
      await page.waitForTimeout(8000);
      await screenshot(page, '09-camera-manual-recording');

      // Stop
      const stopBtn2 = page.getByRole('button', { name: /stop/i });
      if (await stopBtn2.isVisible()) {
        await stopBtn2.click();
        await page.waitForTimeout(2000);
      }
    }

    // Disconnect
    const disconnectBtn2 = page.getByRole('button', { name: /disconnect/i });
    if (await disconnectBtn2.isVisible()) {
      await disconnectBtn2.click();
      await page.waitForTimeout(500);
    }

    // ----------------------------------------------------------------
    // 11. RECORDINGS LIST
    // ----------------------------------------------------------------
    await clickTab(page, 'Recordings');
    // Wait for the recordings API to respond
    await page.waitForTimeout(2000);
    await screenshot(page, '10-recordings-list');

    // ----------------------------------------------------------------
    // 12. PLAY A RECORDING
    // ----------------------------------------------------------------
    const playBtn = page.getByRole('button', { name: /play/i }).first();
    if (await playBtn.isVisible()) {
      await playBtn.click();
      await page.waitForTimeout(3000);
      await screenshot(page, '11-recording-player');

      // Go back to list
      const backBtn = page.getByRole('button', { name: /back/i });
      if (await backBtn.isVisible()) {
        await backBtn.click();
        await page.waitForTimeout(500);
      }
    }

    // ----------------------------------------------------------------
    // 13. ADVANCED SETTINGS
    // ----------------------------------------------------------------
    await clickTab(page, 'Config');
    await page.waitForTimeout(500);

    const advancedToggle = page.getByText(/show advanced settings/i);
    if (await advancedToggle.isVisible()) {
      await advancedToggle.click();
      await page.waitForTimeout(600);
      await screenshot(page, '12-config-advanced');
    }
  });
});
