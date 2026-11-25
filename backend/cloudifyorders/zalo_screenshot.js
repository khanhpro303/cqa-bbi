async ({ browser }) => {
  const settings = __CQA_SCREENSHOT_INPUT__
  const context = await browser.newContext({
    storageState: settings.storageState,
    viewport: { width: 1440, height: 1080 },
  })
  try {
    const page = await context.newPage()
    const chatURL = new URL('https://oa.zalo.me/chat')
    chatURL.searchParams.set('gid', settings.groupID)
    chatURL.searchParams.set('oaid', settings.oaID)
    const matchesGroup = () => {
      const active = new URL(page.url())
      return active.origin === chatURL.origin && active.pathname === chatURL.pathname &&
        active.searchParams.get('gid') === settings.groupID && active.searchParams.get('oaid') === settings.oaID
    }
    await page.goto(chatURL.toString(), { waitUntil: 'domcontentloaded', timeout: 12000 })
    if (!matchesGroup()) throw new Error('OA Manager login or group access required')

    // OA Manager uses a timestamp-prefixed DOM ID. The timestamps were
    // obtained from official GMF history using the exact recorded message IDs.
    const conversation = page.locator('#content_chat')
    const sent = conversation.locator(`[data-message-id^="${settings.sentAt}."]`)
    const reply = conversation.locator(`[data-message-id^="${settings.replyAt}."]`)
    await reply.evaluate(element => element.scrollIntoView({ block: 'center', behavior: 'instant' }), null, { timeout: 7000 })
    if (settings.sentType === 'photo') {
      await sent.evaluate(async element => {
        const images = [...element.querySelectorAll('img')]
        if (images.length === 0) throw new Error('The original order image is missing')
        await Promise.all(images.map(image => image.decode()))
      }, null, { timeout: 5000 })
    }

    const normalize = text => text.normalize('NFC').replace(/\s+/g, ' ').trim()
    async function bounds() {
      if (!matchesGroup()) throw new Error('Zalo group or OA changed during capture')
      if (settings.sentType === 'text' && !normalize(await sent.innerText({ timeout: 2000 })).includes(normalize(settings.sentText))) {
        throw new Error('The visible order message does not match official history')
      }
      if (!normalize(await reply.innerText({ timeout: 2000 })).includes(normalize(settings.replyText))) {
        throw new Error('The visible reply does not match the recorded confirmation')
      }
      const chatBox = await conversation.boundingBox()
      const sentBox = await sent.boundingBox({ timeout: 2000 })
      const replyBox = await reply.boundingBox({ timeout: 2000 })
      if (!chatBox || !sentBox || !replyBox) throw new Error('Both Zalo messages must be visible')
      const x = Math.floor(Math.min(sentBox.x, replyBox.x))
      const y = Math.floor(Math.min(sentBox.y, replyBox.y))
      const right = Math.ceil(Math.max(sentBox.x + sentBox.width, replyBox.x + replyBox.width))
      const bottom = Math.ceil(Math.max(sentBox.y + sentBox.height, replyBox.y + replyBox.height))
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
