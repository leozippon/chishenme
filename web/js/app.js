const $ = (sel, root = document) => root.querySelector(sel)
const view = () => $('#view')
const state = {
  me: null,
  tab: 'week',
  from: todayISO(),
  selected: todayISO(),
  dishes: [],
  pantry: [],
}

document.addEventListener('click', onClick)
document.addEventListener('submit', onSubmit)
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') closeSheet()
})
boot()

async function boot() {
  try {
    state.me = await api('/api/me')
    await reload()
    showApp()
  } catch {
    showLogin()
  } finally {
    $('#boot').hidden = true
  }
}

function showLogin() {
  state.me = null
  $('#app').hidden = true
  $('#login').hidden = false
}

function showApp() {
  $('#login').hidden = true
  $('#app').hidden = false
}

async function reload() {
  const to = addDays(state.from, 6)
  const [week, pantry] = await Promise.all([
    api('/api/week?from=' + state.from + '&to=' + to),
    api('/api/pantry'),
  ])
  state.dishes = week.dishes || []
  state.pantry = pantry || []
  if (state.selected < state.from || state.selected > to) state.selected = state.from
  render()
}

function render() {
  const groups = shopGroups()
  const badge = $('#shop-badge')
  badge.hidden = groups.length === 0
  badge.textContent = groups.length ? String(groups.length) : ''
  for (const tab of document.querySelectorAll('.tab')) {
    tab.classList.toggle('on', tab.dataset.tab === state.tab)
  }
  if (state.tab === 'week') renderWeek()
  else if (state.tab === 'shop') renderShop(groups)
  else renderPantry()
}

function renderWeek() {
  const days = Array.from({ length: 7 }, (_, i) => addDays(state.from, i))
  const today = todayISO()
  view().innerHTML = `
    <div class="top">
      <div>
        <h1 class="title">吃什么</h1>
        <p class="sub">${esc(pretty(state.from))} – ${esc(pretty(addDays(state.from, 6)))}</p>
      </div>
      <button type="button" class="icon-btn" data-action="settings" aria-label="设置">⚙</button>
    </div>
    <div class="week-nav">
      <button type="button" class="btn" data-action="shift" data-by="-7">上一周</button>
      <button type="button" class="btn" data-action="today-week">回到今天</button>
      <button type="button" class="btn" data-action="shift" data-by="7">下一周</button>
    </div>
    <div class="days">
      ${days.map((day) => `
        <button type="button" class="day${day === state.selected ? ' on' : ''}${day === today ? ' today' : ''}" data-action="select-day" data-day="${day}">
          <span class="w">${weekday(day)}</span>
          <span class="n">${Number(day.slice(8))}</span>
        </button>`).join('')}
    </div>
    ${['早', '午', '晚'].map((slot) => slotBlock(slot)).join('')}
  `
}

function slotBlock(slot) {
  const dishes = state.dishes.filter((dish) => dish.day === state.selected && dish.slot === slot)
  const body = dishes.length
    ? dishes.map(dishCard).join('')
    : `<p class="empty">还没安排</p>`
  return `<section class="slot">
    <div class="slot-h">
      <h2>${slotName(slot)}</h2>
      <button type="button" class="btn" data-action="add-dish" data-day="${state.selected}" data-slot="${slot}">加一道</button>
    </div>
    ${body}
  </section>`
}

function dishCard(dish) {
  const items = dish.ingredients || []
  return `<article class="dish">
    <div class="dish-h">
      <h3>${esc(dish.name)}</h3>
      <button type="button" class="text-btn" data-action="edit-dish" data-id="${dish.id}">编辑</button>
    </div>
    ${dish.servings ? `<p class="muted">${esc(dish.servings)}</p>` : ''}
    ${dish.steps ? `<p class="steps">${esc(dish.steps)}</p>` : ''}
    ${items.map((item) => `
      <div class="line${item.ready ? ' ready' : ''}">
        <button type="button" class="tick${item.ready ? ' on' : ''}" data-action="toggle" data-id="${item.id}" aria-pressed="${item.ready}" aria-label="${item.ready ? '已备好' : '还要买'} ${esc(item.name)}"></button>
        <span class="name">${esc(item.name)}</span>
        <span class="amt">${item.amount} ${unitLabel(item.unit)}</span>
        ${item.ready ? `<button type="button" class="text-btn" data-action="return" data-id="${item.id}" data-dish="${dish.id}">放回</button>` : '<span></span>'}
      </div>`).join('')}
    <div class="dish-actions">
      <button type="button" class="text-btn" data-action="allocate" data-id="${dish.id}">从家里放入</button>
      <button type="button" class="text-btn" data-action="delete-dish" data-id="${dish.id}">删除</button>
    </div>
  </article>`
}

function renderShop(groups) {
  const body = groups.length
    ? groups.map((group) => `
      <article class="group">
        <div class="group-h">
          <strong>${esc(group.name)}</strong>
          <span class="amt">${group.amount} ${unitLabel(group.unit)}</span>
        </div>
        <ul>${group.lines.map((line) => `<li>${esc(pretty(line.day))} ${slotName(line.slot)} · ${esc(line.dish)} ${line.amount}${unitLabel(group.unit)}</li>`).join('')}</ul>
        <button type="button" class="btn" data-action="buy" data-ids="${group.ids.join(',')}">买好了，打勾</button>
      </article>`).join('')
    : `<p class="empty">这 7 天要买的都齐了。</p>`
  view().innerHTML = `
    <div class="top">
      <div>
        <h1 class="title">采购</h1>
        <p class="sub">${esc(pretty(state.from))} – ${esc(pretty(addDays(state.from, 6)))} 还要买的</p>
      </div>
      <button type="button" class="icon-btn" data-action="settings" aria-label="设置">⚙</button>
    </div>
    ${body}`
}

function renderPantry() {
  const rows = state.pantry.length
    ? state.pantry.map((item) => `
      <article class="pantry-card">
        <div class="pantry-h">
          <strong>${esc(item.name)}</strong>
          <span class="amt">${item.amount} ${unitLabel(item.unit)}</span>
        </div>
        <div class="pantry-actions">
          <button type="button" class="text-btn" data-action="place" data-id="${item.id}">放到菜里</button>
          <button type="button" class="text-btn" data-action="edit-pantry" data-id="${item.id}">修改</button>
          <button type="button" class="text-btn" data-action="delete-pantry" data-id="${item.id}">删除</button>
        </div>
      </article>`).join('')
    : `<p class="empty">家里还没有记下剩余食材。</p>`
  view().innerHTML = `
    <div class="top">
      <div>
        <h1 class="title">家里</h1>
        <p class="sub">现在手头有的，按名字和份量记。</p>
      </div>
      <button type="button" class="icon-btn" data-action="settings" aria-label="设置">⚙</button>
    </div>
    <form id="pantry-form" class="composer">
      <input name="name" placeholder="食材" maxlength="20" aria-label="食材" required>
      <input name="amount" inputmode="numeric" placeholder="份量" aria-label="份量" required>
      <select name="unit" aria-label="单位">${unitOptions('g')}</select>
      <button class="btn primary" type="submit">记下</button>
    </form>
    ${rows}`
}

function onClick(event) {
  const node = event.target.closest('[data-action]')
  if (!node) return
  const action = node.dataset.action
  if (action === 'close') closeSheet()
  else if (action === 'tab') { state.tab = node.dataset.tab; render() }
  else if (action === 'select-day') { state.selected = node.dataset.day; render() }
  else if (action === 'shift') shiftWeek(Number(node.dataset.by))
  else if (action === 'today-week') { state.from = todayISO(); state.selected = state.from; reload().catch(fail) }
  else if (action === 'settings') openSettings()
  else if (action === 'add-dish') openDish(null, node.dataset.day, node.dataset.slot)
  else if (action === 'edit-dish') openDish(dishById(node.dataset.id))
  else if (action === 'delete-dish') confirmDeleteDish(node.dataset.id)
  else if (action === 'toggle') toggle(node.dataset.id)
  else if (action === 'allocate') openAllocate(dishById(node.dataset.id))
  else if (action === 'return') openReturn(node.dataset.id, node.dataset.dish)
  else if (action === 'place') openPlace(pantryById(node.dataset.id))
  else if (action === 'edit-pantry') openPantryEdit(pantryById(node.dataset.id))
  else if (action === 'delete-pantry') confirmDeletePantry(node.dataset.id)
  else if (action === 'buy') buy(node.dataset.ids)
  else if (action === 'generate') generate(node)
  else if (action === 'add-ing') $('#ing-list')?.insertAdjacentHTML('beforeend', ingRow())
  else if (action === 'remove-ing') node.closest('.ing-edit')?.remove()
  else if (action === 'confirm') runConfirm()
  else if (action === 'theme') setTheme(node.dataset.theme)
  else if (action === 'logout') logout()
}

function onSubmit(event) {
  const form = event.target
  if (form.id === 'login-form') { event.preventDefault(); login(form) }
  else if (form.id === 'dish-form') { event.preventDefault(); saveDish(form) }
  else if (form.id === 'pantry-form') { event.preventDefault(); addPantry(form) }
  else if (form.id === 'pantry-edit') { event.preventDefault(); savePantry(form) }
  else if (form.id === 'return-form') { event.preventDefault(); sendReturn(form) }
  else if (form.id === 'password-form') { event.preventDefault(); changePassword(form) }
  else if (form.dataset.action === 'do-allocate') { event.preventDefault(); sendAllocate(form) }
  else if (form.id === 'place-form') { event.preventDefault(); sendPlace(form) }
}

function formValue(form, name) {
  const item = form.elements.namedItem(name)
  return item ? String(item.value ?? '').trim() : ''
}

async function login(form) {
  const error = $('#login-error')
  error.hidden = true
  try {
    state.me = await api('/api/login', {
      method: 'POST',
      body: { username: formValue(form, 'username'), password: formValue(form, 'password') },
    })
    state.from = todayISO()
    state.selected = state.from
    await reload()
    showApp()
  } catch (err) {
    error.hidden = false
    error.textContent = err.message
  }
}

async function shiftWeek(by) {
  state.from = addDays(state.from, by)
  try { await reload() } catch (err) { fail(err) }
}

function openDish(dish, day, slot) {
  openSheet(dish ? '编辑这道菜' : '加一道菜', dishForm(dish, day, slot))
}

function dishForm(dish, day, slot) {
  const items = dish?.ingredients?.length ? dish.ingredients : [null]
  const recipe = state.me?.recipe
    ? `<button type="button" class="btn" data-action="generate">按菜名生成食材</button>
       <p class="hint">会参考家里已有的食材对齐名字。生成后可以改，保存才写进去。</p>`
    : `<p class="hint">菜谱生成还没配置，直接写下食材就行。</p>`
  return `<form id="dish-form" data-id="${dish ? dish.id : ''}">
    <label class="field">日期<input type="date" name="day" required value="${esc(dish?.day || day)}"></label>
    <div class="segment">${['早', '午', '晚'].map((item) => `
      <label class="seg"><input type="radio" name="slot" value="${item}" ${(dish?.slot || slot) === item ? 'checked' : ''}>${slotName(item)}</label>`).join('')}</div>
    <label class="field">菜名<input name="name" required maxlength="24" value="${esc(dish?.name || '')}" placeholder="例如 番茄炒蛋"></label>
    ${recipe}
    <label class="field">几人份<input name="servings" maxlength="16" value="${esc(dish?.servings || '')}" placeholder="2人"></label>
    <label class="field">做法<textarea name="steps" maxlength="800">${esc(dish?.steps || '')}</textarea></label>
    <div class="ing-list" id="ing-list">${items.map((item) => ingRow(item || {})).join('')}</div>
    <button type="button" class="btn quiet" data-action="add-ing">再加一项食材</button>
    <p class="sheet-error" hidden></p>
    <button class="btn primary" type="submit">保存</button>
  </form>`
}

function ingRow(item = {}) {
  return `<div class="ing-edit">
    <input class="ing-name" aria-label="食材名" maxlength="20" placeholder="食材" value="${esc(item.name || '')}">
    <input class="ing-amount" aria-label="份量" inputmode="numeric" placeholder="份量" value="${item.amount ?? ''}">
    <select class="ing-unit" aria-label="单位">${unitOptions(item.unit || 'g')}</select>
    <label class="checkline"><input class="ing-ready" type="checkbox" ${item.ready ? 'checked' : ''}>已有</label>
    <button type="button" class="text-btn" data-action="remove-ing">移除</button>
  </div>`
}

async function generate(button) {
  const form = $('#dish-form')
  const name = formValue(form, 'name')
  if (!name) { sheetError('先写下菜名'); return }
  button.disabled = true
  const previous = button.textContent
  button.textContent = '正在生成…'
  try {
    const recipe = await api('/api/recipes', { method: 'POST', body: { name } })
    const servings = form.elements.namedItem('servings')
    const steps = form.elements.namedItem('steps')
    if (servings && !servings.value.trim()) servings.value = recipe.servings || ''
    if (steps && !steps.value.trim()) steps.value = recipe.steps || ''
    const list = $('#ing-list')
    list.innerHTML = (recipe.ingredients || []).map(ingRow).join('') || ingRow()
    sheetError('')
    toast('食材已填上，确认后点保存')
  } catch (err) {
    sheetError(err.message)
  } finally {
    button.disabled = false
    button.textContent = previous
  }
}

async function saveDish(form) {
  const ingredients = []
  for (const row of form.querySelectorAll('.ing-edit')) {
    const name = row.querySelector('.ing-name').value.trim()
    const amountText = row.querySelector('.ing-amount').value.trim()
    if (!name && !amountText) continue
    const amount = Number(amountText)
    if (!name || !Number.isInteger(amount) || amount < 1) {
      sheetError('每样食材都要有名字，份量是正整数')
      return
    }
    ingredients.push({
      name,
      amount,
      unit: row.querySelector('.ing-unit').value,
      ready: row.querySelector('.ing-ready').checked,
    })
  }
  const body = {
    day: formValue(form, 'day'),
    slot: formValue(form, 'slot'),
    name: formValue(form, 'name'),
    servings: formValue(form, 'servings'),
    steps: formValue(form, 'steps'),
    ingredients,
  }
  const id = form.dataset.id
  try {
    await api(id ? '/api/dishes/' + id : '/api/dishes', { method: id ? 'PUT' : 'POST', body })
    closeSheet()
    state.selected = body.day
    if (body.day < state.from || body.day > addDays(state.from, 6)) state.from = startOfWeek(body.day)
    await reload()
    toast(id ? '已保存' : '已加上')
  } catch (err) {
    sheetError(err.message)
  }
}

function confirmDeleteDish(id) {
  const dish = dishById(id)
  if (!dish) return
  ask('删除这道菜', `删除「${dish.name}」和它下面的食材。家里的库存不会变。`, '删除', async () => {
    await api('/api/dishes/' + id, { method: 'DELETE' })
    await reload()
    toast('已删除')
  })
}

async function toggle(id) {
  const item = ingredientById(id)
  if (!item) return
  try {
    await api('/api/ingredients/ready', { method: 'POST', body: { ids: [Number(id)], ready: !item.ready } })
    await reload()
  } catch (err) { fail(err) }
}

async function buy(ids) {
  try {
    await api('/api/ingredients/ready', {
      method: 'POST',
      body: { ids: ids.split(',').map(Number), ready: true },
    })
    await reload()
    toast('已打勾')
  } catch (err) { fail(err) }
}

function openAllocate(dish) {
  if (!dish) return
  if (!state.pantry.length) {
    openSheet('从家里放入', `<p class="sheet-text">家里还没有记下剩余食材。先到「家里」记一笔，再放进这道菜。</p>`)
    return
  }
  openSheet('从家里放入', `
    <p class="sheet-text">放入「${esc(dish.name)}」。同名而且单位相同的「还要买」会减去这一笔，多出来的留在菜上，标成已有。</p>
    ${state.pantry.map((item) => `
      <form class="place-row" data-action="do-allocate" data-pantry="${item.id}" data-dish="${dish.id}">
        <div><strong>${esc(item.name)}</strong> <span class="muted">${item.amount} ${unitLabel(item.unit)}</span></div>
        <input name="amount" inputmode="numeric" value="${suggest(item, dish)}" aria-label="放入多少${esc(item.name)}">
        <button class="btn" type="submit">放入</button>
      </form>`).join('')}`)
}

async function sendAllocate(form) {
  const amount = Number(formValue(form, 'amount'))
  if (!Number.isInteger(amount) || amount < 1) { toast('份量要是正整数'); return }
  try {
    await api('/api/allocate', {
      method: 'POST',
      body: { pantry_id: Number(form.dataset.pantry), dish_id: Number(form.dataset.dish), amount },
    })
    closeSheet()
    await reload()
    toast('已从家里放入')
  } catch (err) { toast(err.message) }
}

function openReturn(ingredientID, dishID) {
  const dish = dishById(dishID)
  const item = (dish?.ingredients || []).find((row) => String(row.id) === String(ingredientID))
  if (!item) return
  openSheet('放回家里', `
    <form id="return-form" data-id="${item.id}">
      <p class="sheet-text">把「${esc(dish.name)}」里已备好的${esc(item.name)}放回家里。还要买的那一行不会自动加回来。</p>
      <label class="field">放回多少
        <input name="amount" inputmode="numeric" value="${item.amount}">
      </label>
      <p class="hint">最多 ${item.amount} ${unitLabel(item.unit)}</p>
      <button class="btn primary" type="submit">放回</button>
    </form>`)
}

async function sendReturn(form) {
  const amount = Number(formValue(form, 'amount'))
  if (!Number.isInteger(amount) || amount < 1) { sheetError('份量要是正整数'); return }
  try {
    await api('/api/return', { method: 'POST', body: { ingredient_id: Number(form.dataset.id), amount } })
    closeSheet()
    await reload()
    toast('已放回家里')
  } catch (err) { sheetError(err.message) }
}

async function addPantry(form) {
  const amount = Number(formValue(form, 'amount'))
  if (!Number.isInteger(amount) || amount < 1) { toast('份量要是正整数'); return }
  try {
    await api('/api/pantry', {
      method: 'POST',
      body: { name: formValue(form, 'name'), amount, unit: formValue(form, 'unit') },
    })
    form.reset()
    await reload()
    toast('已记下')
  } catch (err) { fail(err) }
}

function openPantryEdit(item) {
  if (!item) return
  openSheet('修改库存', `
    <form id="pantry-edit" data-id="${item.id}">
      <label class="field">食材<input name="name" maxlength="20" required value="${esc(item.name)}"></label>
      <label class="field">份量<input name="amount" inputmode="numeric" required value="${item.amount}"></label>
      <label class="field">单位<select name="unit">${unitOptions(item.unit)}</select></label>
      <p class="hint">如果家里已经有同名同单位的一条，两笔会合成一笔。</p>
      <p class="sheet-error" hidden></p>
      <button class="btn primary" type="submit">保存</button>
    </form>`)
}

async function savePantry(form) {
  const amount = Number(formValue(form, 'amount'))
  if (!Number.isInteger(amount) || amount < 1) { sheetError('份量要是正整数'); return }
  try {
    await api('/api/pantry/' + form.dataset.id, {
      method: 'PUT',
      body: { name: formValue(form, 'name'), amount, unit: formValue(form, 'unit') },
    })
    closeSheet()
    await reload()
    toast('已保存')
  } catch (err) { sheetError(err.message) }
}

function confirmDeletePantry(id) {
  const item = pantryById(id)
  if (!item) return
  ask('删除库存', `家里不再记下「${item.name}」。已经放到菜里的不会收回来。`, '删除', async () => {
    await api('/api/pantry/' + id, { method: 'DELETE' })
    await reload()
    toast('已删除')
  })
}

function openPlace(item) {
  if (!item) return
  if (!state.dishes.length) {
    openSheet('放到菜里', `<p class="sheet-text">这 7 天还没有菜。先去排一顿，再把${esc(item.name)}放进去。</p>`)
    return
  }
  const first = state.dishes[0]
  openSheet('放到菜里', `
    <form id="place-form" data-id="${item.id}">
      <p class="sheet-text">${esc(item.name)}，家里有 ${item.amount} ${unitLabel(item.unit)}。</p>
      <div class="stack" id="place-dishes">
        ${state.dishes.map((dish) => `
          <button type="button" class="choice${dish.id === first.id ? ' on' : ''}" data-dish="${dish.id}">
            ${esc(pretty(dish.day))} ${slotName(dish.slot)} · ${esc(dish.name)}
          </button>`).join('')}
      </div>
      <label class="field">放多少<input name="amount" inputmode="numeric" value="${suggest(item, first)}"></label>
      <p class="hint">只会放到上面选中的那一道。同名同单位还要买的份量会减少。</p>
      <p class="sheet-error" hidden></p>
      <button class="btn primary" type="submit">放入</button>
    </form>`)
  $('#place-dishes')?.addEventListener('click', (event) => {
    const choice = event.target.closest('[data-dish]')
    if (!choice) return
    for (const button of $('#place-dishes').querySelectorAll('.choice')) button.classList.remove('on')
    choice.classList.add('on')
    const dish = dishById(choice.dataset.dish)
    const input = $('#place-form')?.elements.namedItem('amount')
    if (dish && input) input.value = suggest(item, dish)
  })
}

async function sendPlace(form) {
  const chosen = $('#place-dishes .choice.on')
  const amount = Number(formValue(form, 'amount'))
  if (!chosen) { sheetError('先选一道菜'); return }
  if (!Number.isInteger(amount) || amount < 1) { sheetError('份量要是正整数'); return }
  try {
    await api('/api/allocate', {
      method: 'POST',
      body: { pantry_id: Number(form.dataset.id), dish_id: Number(chosen.dataset.dish), amount },
    })
    closeSheet()
    await reload()
    toast('已放到菜里')
  } catch (err) { sheetError(err.message) }
}

function openSettings() {
  const mode = localStorage.getItem('chishenme-theme') || 'auto'
  openSheet('设置', `
    <p class="sheet-text">当前是 ${esc(state.me.username)}。菜谱生成${state.me.recipe ? '可用，写新菜时可以按菜名填食材。' : '还没配置。'}</p>
    <div class="segment" aria-label="外观">
      ${[['auto', '跟随系统'], ['light', '浅色'], ['dark', '深色']].map(([value, label]) => `
        <button type="button" class="choice${mode === value ? ' on' : ''}" data-action="theme" data-theme="${value}">${label}</button>`).join('')}
    </div>
    <form id="password-form">
      <label class="field">当前密码<input name="current" type="password" autocomplete="current-password" required></label>
      <label class="field">新密码<input name="next" type="password" autocomplete="new-password" required></label>
      <p class="sheet-error" hidden></p>
      <button class="btn" type="submit">修改密码</button>
    </form>
    <button type="button" class="btn danger" data-action="logout">退出登录</button>`)
}

function setTheme(mode) {
  localStorage.setItem('chishenme-theme', mode)
  const dark = mode === 'dark' || (mode !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
  document.querySelector('meta[name="theme-color"]').setAttribute('content', dark ? '#171410' : '#f4efe6')
  for (const button of document.querySelectorAll('[data-action="theme"]')) {
    button.classList.toggle('on', button.dataset.theme === mode)
  }
}

async function changePassword(form) {
  try {
    await api('/api/me/password', {
      method: 'PUT',
      body: { current: formValue(form, 'current'), next: formValue(form, 'next') },
    })
    closeSheet()
    showLogin()
    toast('密码已更新，请重新登录')
  } catch (err) { sheetError(err.message) }
}

async function logout() {
  try { await api('/api/logout', { method: 'POST' }) } catch { /* the cookie is cleared locally anyway */ }
  closeSheet()
  showLogin()
}

function ask(title, text, label, action) {
  state.pending = action
  openSheet(title, `
    <p class="sheet-text">${esc(text)}</p>
    <div class="row-actions">
      <button type="button" class="btn" data-action="close">取消</button>
      <button type="button" class="btn danger" data-action="confirm">${esc(label)}</button>
    </div>`)
}

async function runConfirm() {
  const action = state.pending
  state.pending = null
  closeSheet()
  if (!action) return
  try { await action() } catch (err) { fail(err) }
}

function openSheet(title, body) {
  const root = $('#sheet')
  root.hidden = false
  root.innerHTML = `
    <div class="backdrop" data-action="close"></div>
    <div class="sheet-card" role="dialog" aria-modal="true" aria-label="${esc(title)}">
      <div class="sheet-bar"><h2>${esc(title)}</h2>
        <button type="button" class="icon-btn" data-action="close" aria-label="关闭">×</button>
      </div>
      ${body}
    </div>`
}

function closeSheet() {
  state.pending = null
  const root = $('#sheet')
  root.hidden = true
  root.innerHTML = ''
}

function sheetError(message) {
  const node = $('#sheet .sheet-error')
  if (!node) { if (message) toast(message); return }
  node.hidden = !message
  node.textContent = message || ''
}

let toastTimer = 0
function toast(message) {
  const node = $('#toast')
  node.hidden = false
  node.textContent = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { node.hidden = true }, 2400)
}

function fail(err) { toast(err.message || '没有完成') }

function shopGroups() {
  const map = new Map()
  for (const dish of state.dishes) {
    for (const item of dish.ingredients || []) {
      if (item.ready) continue
      const key = item.name + '\0' + item.unit
      let group = map.get(key)
      if (!group) {
        group = { name: item.name, unit: item.unit, amount: 0, ids: [], lines: [] }
        map.set(key, group)
      }
      group.amount += item.amount
      group.ids.push(item.id)
      group.lines.push({ day: dish.day, slot: dish.slot, dish: dish.name, amount: item.amount })
    }
  }
  return [...map.values()]
}

function suggest(item, dish) {
  const need = (dish.ingredients || []).find((row) => !row.ready && row.name === item.name && row.unit === item.unit)
  if (!need) return item.amount
  return Math.min(item.amount, need.amount)
}

function dishById(id) { return state.dishes.find((dish) => String(dish.id) === String(id)) }
function pantryById(id) { return state.pantry.find((item) => String(item.id) === String(id)) }
function ingredientById(id) {
  for (const dish of state.dishes) {
    const found = (dish.ingredients || []).find((item) => String(item.id) === String(id))
    if (found) return found
  }
  return null
}

function unitOptions(selected) {
  return [['g', '克'], ['ml', '毫升'], ['个', '个']]
    .map(([value, label]) => `<option value="${value}" ${value === selected ? 'selected' : ''}>${label}</option>`)
    .join('')
}
function unitLabel(unit) { return unit === 'ml' ? '毫升' : unit === '个' ? '个' : '克' }
function slotName(slot) { return slot === '早' ? '早餐' : slot === '午' ? '午餐' : '晚餐' }

function todayISO() { return formatDate(new Date()) }
function formatDate(date) {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}
function addDays(iso, count) {
  const [year, month, day] = iso.split('-').map(Number)
  return formatDate(new Date(year, month - 1, day + count))
}
function startOfWeek(iso) {
  const [year, month, day] = iso.split('-').map(Number)
  const date = new Date(year, month - 1, day)
  const offset = (date.getDay() + 6) % 7
  date.setDate(date.getDate() - offset)
  return formatDate(date)
}
function pretty(iso) {
  const [, month, day] = iso.split('-')
  return `${Number(month)}月${Number(day)}日`
}
function weekday(iso) {
  const [year, month, day] = iso.split('-').map(Number)
  return '日一二三四五六'[new Date(year, month - 1, day).getDay()]
}
function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[char]))
}

async function api(path, opts = {}) {
  const response = await fetch(path, {
    method: opts.method || 'GET',
    headers: opts.body ? { 'Content-Type': 'application/json' } : undefined,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
    credentials: 'same-origin',
  })
  const text = await response.text()
  let data = null
  if (text) {
    try { data = JSON.parse(text) } catch { data = null }
  }
  if (response.status === 401 && path !== '/api/login') {
    showLogin()
    throw new Error(data?.error || '请先登录')
  }
  if (!response.ok) throw new Error(data?.error || '没有完成')
  return data
}
