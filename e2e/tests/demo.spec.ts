import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

async function ready(page: Page) {
  await page.goto('./');
  await expect(page.getByTestId('line')).toBeEnabled({ timeout: 20_000 }); // WebAssembly загрузился
}

async function run(page: Page, cmd: string) {
  await page.getByTestId('line').fill(cmd);
  await page.getByTestId('line').press('Enter');
}

test('ручной ввод: SET, GET, ошибка WRONGTYPE', async ({ page }) => {
  await ready(page);
  await run(page, 'SET greeting "hello world" EX 30');
  await run(page, 'GET greeting');
  await run(page, 'LPUSH greeting x');
  const term = page.getByTestId('term');
  await expect(term).toContainText('"hello world"');
  await expect(term).toContainText('(error) WRONGTYPE Operation against a key holding the wrong kind of value');
  await run(page, 'TTL greeting');
  await expect(term).toContainText('(integer) 30');
});

test('история команд стрелкой вверх', async ({ page }) => {
  await ready(page);
  await run(page, 'PING');
  await run(page, 'ECHO hi');
  await page.getByTestId('line').press('ArrowUp');
  await expect(page.getByTestId('line')).toHaveValue('ECHO hi');
  await page.getByTestId('line').press('ArrowUp');
  await expect(page.getByTestId('line')).toHaveValue('PING');
});

test('сценарии: транзакция и ошибки', async ({ page }) => {
  await ready(page);
  await page.getByRole('button', { name: 'Транзакция' }).click();
  const term = page.getByTestId('term');
  await expect(term).toContainText('QUEUED');
  await expect(term).toContainText('EXECABORT'); // вторая транзакция содержит неизвестную команду
  await page.getByRole('button', { name: 'Ошибки' }).click();
  await expect(term).toContainText('(error) ERR value is not an integer or out of range');
  await expect(term).toContainText('(error) ERR invalid expire time');
});

test('сценарий «Строки и TTL»: счётчики и NX', async ({ page }) => {
  await ready(page);
  await page.getByRole('button', { name: 'Строки и TTL' }).click();
  const term = page.getByTestId('term');
  await expect(term).toContainText('INCRBY visits 10');
  await expect(term).toContainText('(integer) 11');
  await expect(term).toContainText('SET greeting new NX');
});

test('незакрытая кавычка не ломает терминал', async ({ page }) => {
  await ready(page);
  await run(page, 'SET k "unclosed');
  await expect(page.getByTestId('term')).toContainText('unbalanced quotes');
  await run(page, 'PING');
  await expect(page.getByTestId('term')).toContainText('PONG');
});

test('нет горизонтальной прокрутки на узком экране', async ({ page }) => {
  await ready(page);
  await page.setViewportSize({ width: 360, height: 740 });
  await page.getByRole('button', { name: 'Хэши и множества' }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
});

for (const scheme of ['light', 'dark'] as const) {
  test(`доступность (axe), ${scheme === 'light' ? 'светлая' : 'тёмная'} тема`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await ready(page);
    const check = async (label: string) => {
      const res = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
      const bad = res.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
      expect(bad.map((v) => `${v.id}: ${v.nodes[0]?.html}`), label).toEqual([]);
    };
    await check('пустой терминал');
    await page.getByRole('button', { name: 'Хэши и множества' }).click();
    await check('после сценария');
    await page.getByRole('button', { name: 'Ошибки' }).click();
    await check('ошибки в терминале');
  });
}
