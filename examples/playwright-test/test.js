const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const page = await browser.newPage();

  await page.setContent(`
    <html>
      <body style="font-family: sans-serif; padding: 40px;">
        <h1>Playwright is working!</h1>
        <p>Rendered by headless Chromium inside a Moat container.</p>
      </body>
    </html>
  `);

  await page.screenshot({ path: "screenshot.png" });
  await browser.close();

  console.log("  Page rendered successfully");
  console.log("  Screenshot saved to screenshot.png");
})();
