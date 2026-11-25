async ({ browser }) => {
  const settings = __CQA_SCREENSHOT_INPUT__
  const context = await browser.newContext({
    storageState: settings.storageState,
    viewport: { width: 1440, height: 1080 },
  })
  try {
    const page = await context.newPage()
    await page.goto('https://oa.zalo.me/chatv2', { waitUntil: 'domcontentloaded', timeout: 12000 })
    const atOAManager = () => {
      const url = new URL(page.url())
      return url.origin === 'https://oa.zalo.me' &&
        (url.pathname === '/chatv2' || url.pathname.startsWith('/chatv2/'))
    }
    if (!atOAManager()) throw new Error('OA Manager login required')
    const group = page.locator(settings.openGroup)
    await group.click({ timeout: 5000 })
    if (!atOAManager()) throw new Error('Unexpected OA Manager navigation')
    const conversation = page.locator(settings.conversation)
    await conversation.waitFor({ state: 'visible', timeout: 5000 })
    const reply = conversation.locator(settings.reply)
    await reply.evaluate(element => element.scrollIntoView({ block: 'center', behavior: 'instant' }), null, { timeout: 5000 })
    const original = conversation.locator(settings.original)
    await original.evaluate(element => Promise.all(
      Array.from(element.querySelectorAll('img'), image => image.decode()),
    ), null, { timeout: 5000 })

    // Resolve both original message elements in the currently selected group.
    // Refuse off-screen or clipped messages, which would yield partial evidence.
    async function bounds() {
      if (!atOAManager()) throw new Error('Unexpected OA Manager navigation')
      const chatBox = await conversation.boundingBox()
      const originalBox = await original.boundingBox({ timeout: 2000 })
      const replyBox = await reply.boundingBox({ timeout: 2000 })
      const normalize = text => text.normalize('NFC').replace(/\s+/g, ' ').trim()
      if (!chatBox || !originalBox || !replyBox ||
          !normalize(await reply.innerText({ timeout: 2000 })).includes(normalize(settings.replyText))) {
        throw new Error('Could not verify the visible Zalo messages')
      }
      const x = Math.floor(Math.min(originalBox.x, replyBox.x))
      const y = Math.floor(Math.min(originalBox.y, replyBox.y))
      const right = Math.ceil(Math.max(originalBox.x + originalBox.width, replyBox.x + replyBox.width))
      const bottom = Math.ceil(Math.max(originalBox.y + originalBox.height, replyBox.y + replyBox.height))
      const viewport = page.viewportSize()
      if (x < Math.max(0, Math.floor(chatBox.x)) || y < Math.max(0, Math.floor(chatBox.y)) ||
          right > Math.min(viewport.width, Math.ceil(chatBox.x + chatBox.width)) ||
          bottom > Math.min(viewport.height, Math.ceil(chatBox.y + chatBox.height))) {
        throw new Error('Both Zalo messages must fit in the visible conversation')
      }
      return { x, y, width: right - x, height: bottom - y }
    }

    const clip = await bounds()
    const screenshot = await page.screenshot({ type: 'png', clip, timeout: 5000 })
    if (JSON.stringify(clip) !== JSON.stringify(await bounds())) {
      throw new Error('Zalo conversation moved while capturing')
    }
    return new Response(screenshot, { headers: { 'content-type': 'image/png' } })
  } finally {
    await context.close()
  }
}
