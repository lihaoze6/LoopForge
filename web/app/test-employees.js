let employeeDialog;

function testEmployeeMemberActions(project) {
  const userId = Number(state.currentUser?.id);
  if (!project || !(userId > 0) || !(isPlatformAdmin() || Number(project.ownerId) === userId)) return '';
  return '<button type="button" class="button hui-btn-soft small" data-test-employee-action="create">＋ 添加虚拟员工</button><button type="button" class="button ghost small" data-test-employee-action="manage">管理自动参与</button>';
}

function employeeProjectPath(projectId) {
  return '/api/projects/' + encodeURIComponent(projectId);
}

function employeeOption(value, label, selected = '') {
  return `<option value="${escapeHTML(value)}"${value === selected ? ' selected' : ''}>${escapeHTML(label)}</option>`;
}

function getEmployeeDialog() {
  if (employeeDialog) return employeeDialog;
  const dialog = document.createElement('dialog');
  dialog.className = 'employee-dialog';
  dialog.setAttribute('aria-labelledby', 'employeeDialogTitle');
  dialog.innerHTML = `<div class="employee-dialog-frame"><header>
    <div><h2 id="employeeDialogTitle"></h2><p id="employeeDialogProject"></p></div>
    <button type="button" class="button hui-icon-btn" data-employee-close aria-label="关闭">×</button>
    </header><div class="employee-dialog-content" id="employeeDialogContent"></div></div>`;
  dialog.querySelector('[data-employee-close]').onclick = () => dialog.close();
  dialog.addEventListener('close', () => {
    dialog.employeeRequest?.abort();
    dialog.querySelector('#employeeDialogContent').replaceChildren();
    dialog.employeeTrigger?.focus();
  });
  dialog.addEventListener('cancel', event => {
    if (dialog.employeeBusy) event.preventDefault();
  });
  document.body.append(dialog);
  employeeDialog = dialog;
  return dialog;
}

async function openTestEmployeeDialog(projectId, mode) {
  const dialog = getEmployeeDialog();
  if (!dialog.open) {
    dialog.employeeTrigger = document.activeElement;
    dialog.showModal();
  }
  dialog.employeeRequest?.abort();
  const request = dialog.employeeRequest = new AbortController();
  const title = dialog.querySelector('h2');
  const host = dialog.querySelector('#employeeDialogContent');
  title.textContent = mode === 'create' ? '添加虚拟员工' : '管理员工自动参与';
  dialog.querySelector('#employeeDialogProject').textContent =
    (state.data.projects || []).find(project => project.id === projectId)?.name || projectId;
  host.innerHTML = '<p class="employee-status" role="status">正在读取项目配置…</p>';
  try {
    const data = await api(employeeProjectPath(projectId) + '/test-employees', {silent: true, signal: request.signal});
    if (request.signal.aborted) return;
    const context = {dialog, title, host, projectId, data, signal: request.signal};
    if (mode === 'create') renderEmployeeEditor(context);
    else renderEmployeeManager(context);
  } catch (error) {
    if (!request.signal.aborted) host.textContent = error.message;
  }
}

function renderEmployeeManager(context) {
  const {title, host, data, projectId, signal} = context;
  title.textContent = '管理员工自动参与';
  const employees = data.employees.map(item => {
    const user = userById(item.userId) || {};
    const name = user.chineseName || user.loginName || '员工 ' + item.userId;
    return `<div class="employee-record"><div class="employee-record-text"><strong>${escapeHTML(name)}</strong>
      <span>${escapeHTML([item.duty, item.model].filter(Boolean).join(' · '))}</span></div>
      <span class="employee-pill${item.paused ? '' : ' is-active'}">${item.paused ? '已暂停' : '可自动参与'}</span>
      <div class="employee-record-actions"><button class="button ghost small" type="button" data-employee-edit="${item.userId}">编辑</button>
      <button class="button ghost small" type="button" data-employee-pause="${item.userId}">${item.paused ? '恢复' : '暂停'}</button></div></div>`;
  }).join('');
  const connections = data.connections.filter(item => !item.employeeId).map(item => `<div class="employee-record"><div class="employee-record-text"><strong>${escapeHTML(item.name)}</strong><span>${escapeHTML(item.models.join('、'))}</span></div>${Number(item.ownerId) === Number(state.currentUser?.id) || isPlatformAdmin() ? `<button type="button" class="button ghost small" data-employee-revoke="${escapeHTML(item.id)}">撤销授权</button>` : ''}</div>`).join('');
  host.innerHTML = `<div class="employee-manager">
    <section><div class="employee-section-head"><h3>员工</h3><button type="button" class="button hui-btn-soft small" data-employee-add>添加虚拟员工</button></div>
      ${employees || '<p class="employee-status">还没有配置由模型参与讨论的员工。</p>'}</section>
    <section><div class="employee-section-head"><h3>已授权模型连接</h3><button type="button" class="button ghost small" data-employee-share>授权现有连接</button></div>
      ${connections || '<p class="employee-status">暂无共享连接。</p>'}</section>
    <section><div class="employee-section-head"><h3>讨论故障</h3><button type="button" class="button ghost small" data-employee-refresh>刷新</button></div>
      <div data-employee-failures role="status"></div></section>
  </div>`;
  host.querySelector('[data-employee-add]').onclick = () => renderEmployeeEditor(context);
  host.querySelector('[data-employee-share]').onclick = () => renderEmployeeShare(context, () => openTestEmployeeDialog(projectId, 'manage'));
  for (const button of host.querySelectorAll('[data-employee-edit]')) {
    button.onclick = () => renderEmployeeEditor(context, data.employees.find(item => item.userId === Number(button.dataset.employeeEdit)));
  }
  for (const button of host.querySelectorAll('[data-employee-pause]')) {
    const item = data.employees.find(employee => employee.userId === Number(button.dataset.employeePause));
    button.onclick = () => employeeButtonRequest(button, async () => {
      await api(employeeProjectPath(projectId) + '/test-employees/' + item.userId + '/pause', {method: 'PUT', body: JSON.stringify({paused: !item.paused})});
      await openTestEmployeeDialog(projectId, 'manage');
    });
  }
  for (const button of host.querySelectorAll('[data-employee-revoke]')) {
    button.onclick = () => employeeButtonRequest(button, async () => {
      await api(employeeProjectPath(projectId) + '/employee-model-connections/' + encodeURIComponent(button.dataset.employeeRevoke), {method: 'DELETE'});
      await openTestEmployeeDialog(projectId, 'manage');
    });
  }
  const refresh = () => renderEmployeeFailures(host.querySelector('[data-employee-failures]'), projectId, signal);
  host.querySelector('[data-employee-refresh]').onclick = refresh;
  refresh();
}

async function renderEmployeeFailures(el, projectId, signal) {
  if (!el) return;
  const path = employeeProjectPath(projectId) + '/employee-discussion-failures';
  el.textContent = '正在读取…';
  try {
    const items = await api(path, {silent: true, signal});
    if (signal.aborted) return;
    el.innerHTML = items.map(item => `<div class="employee-record">
      <div class="employee-record-text"><strong>${escapeHTML(item.employeeName || String(item.userId))} · ${escapeHTML(item.roomName || item.roomId)}</strong>
      <span>${escapeHTML(item.error)} · ${escapeHTML(item.model || '')} · ${escapeHTML(new Date(item.updatedAt).toLocaleString())}</span></div>
      <button type="button" class="button ghost small" data-retry-employee="${item.id}">重试</button></div>`).join('') ||
      '<p class="employee-status">没有失败记录。</p>';
    for (const button of el.querySelectorAll('[data-retry-employee]')) {
      button.onclick = () => employeeButtonRequest(button, async () => {
        await api(path + '/' + encodeURIComponent(button.dataset.retryEmployee) + '/retry', {method: 'POST'});
        await renderEmployeeFailures(el, projectId, signal);
      });
    }
  } catch (error) {
    if (!signal.aborted) el.textContent = error.message;
  }
}

function renderEmployeeEditor(context, employee = null) {
  const {dialog, title, host, data, projectId} = context;
  const selected = data.connections.find(item => item.id === employee?.connectionId);
  const shared = data.connections.filter(item => !item.employeeId);
  title.textContent = employee ? '编辑员工配置' : '添加虚拟员工';
  const account = employee ? '' : `<fieldset><legend>账号</legend><div class="employee-form-grid">
    <label>显示名<input name="displayName" maxlength="64" required placeholder="例如 小林"></label>
    <label>登录名<input name="loginName" maxlength="64" required pattern="[A-Za-z0-9][A-Za-z0-9_.\\-]{1,63}" placeholder="例如 lin.qa" autocomplete="off"></label>
    <label class="employee-span">初始密码<input name="password" type="password" required autocomplete="new-password" minlength="6" maxlength="72"></label>
  </div></fieldset>`;
  host.innerHTML = `<form class="employee-form">${account}
    <fieldset><legend>角色与提示词</legend><div class="employee-form-grid">
      <label>角色<select name="roleId">${employeeOption('', '自定义职责')}${data.roles.map(role => employeeOption(role.id, role.name, employee?.roleId)).join('')}</select></label>
      <label>职责<input name="duty" maxlength="120" value="${escapeHTML(employee?.duty || '')}" placeholder="例如 测试工程师"></label>
      <label class="employee-span">提示词<textarea name="prompt" rows="4" maxlength="8000">${escapeHTML(employee?.prompt ?? '')}</textarea></label>
      <div class="employee-span employee-inline"><button class="button ghost small" type="button" data-inherit-prompt>恢复角色提示词</button><span class="employee-status" data-prompt-mode></span></div>
    </div></fieldset>
    <fieldset><legend>模型</legend>
      <label>连接<select name="connectionId">${shared.map(item => employeeOption(item.id, item.name, employee?.connectionId)).join('')}${selected?.employeeId ? employeeOption(selected.id, '保留当前专用连接', selected.id) : ''}${employeeOption('custom', '配置员工专用连接', !selected && !shared.length ? 'custom' : '')}</select></label>
      <div class="employee-inline"><button type="button" class="button ghost small" data-employee-share>授权现有连接</button></div>
      <div class="employee-form-grid employee-custom" data-employee-custom hidden>
        <label>连接名称<input name="baseName" maxlength="80" placeholder="例如 Magpie"></label>
        <label>协议<select name="protocol"><option value="chat">OpenAI Chat Completions</option><option value="responses">OpenAI Responses</option><option value="messages">Anthropic Messages</option></select></label>
        <label>服务地址<input name="baseUrl" type="url" maxlength="2048" placeholder="https://gateway.example.com/v1"></label>
        <label>API Key<input name="apiKey" type="password" maxlength="4096" autocomplete="new-password" placeholder="仅存储在服务端"></label>
        <label class="employee-span">模型 ID<input name="customModel" maxlength="250" placeholder="填写服务返回的模型 ID"></label>
      </div>
      <label data-employee-model>模型<select name="model"></select></label>
      <div class="employee-inline"><button type="button" class="button ghost small" data-test-connection>测试连接</button><span class="employee-status" data-effective-model></span></div>
      <div class="employee-status" data-test-result role="status"></div>
    </fieldset>
    <p class="employee-form-error" role="alert" hidden></p>
    <footer><button class="button ghost" type="button" data-employee-back>返回</button>
      <button class="button primary" type="submit">${employee ? '保存配置' : '创建并加入项目'}</button></footer>
    </form>`;
  const form = host.querySelector('form');
  const fields = form.elements;
  let promptOverride = employee?.prompt != null;
  let chosenModel = employee?.model || '';
  const updatePrompt = () => {
    const role = data.roles.find(item => item.id === fields.roleId.value);
    if (!promptOverride) fields.prompt.value = role?.prompt || '';
    form.querySelector('[data-prompt-mode]').textContent = promptOverride ? '使用员工自己的提示词' : role ? '继承角色提示词' : '填写提示词或职责';
    form.querySelector('[data-inherit-prompt]').hidden = !role;
  };
  const updateModel = () => {
    const custom = fields.connectionId.value === 'custom';
    const connection = data.connections.find(item => item.id === fields.connectionId.value);
    const role = data.roles.find(item => item.id === fields.roleId.value);
    const models = connection?.models || [];
    const pick = models.includes(chosenModel) ? chosenModel : models.includes(role?.model) ? role.model : models[0];
    fields.model.innerHTML = models.map(id => employeeOption(id, id, pick)).join('');
    fields.model.disabled = custom;
    form.querySelector('[data-employee-model]').hidden = custom;
    form.querySelector('[data-employee-custom]').hidden = !custom;
    for (const name of ['baseName', 'baseUrl', 'customModel']) {
      fields[name].required = custom;
      fields[name].disabled = !custom;
    }
    const summary = custom ? fields.customModel.value.trim() : pick;
    form.querySelector('[data-effective-model]').textContent = summary ? (custom ? fields.protocol.value : connection?.protocol || '') + ' · ' + summary : '';
  };
  const connectionPayload = () => {
    const result = {connectionId: fields.connectionId.value, model: fields.model.value, userId: employee?.userId || 0};
    if (result.connectionId === 'custom') {
      result.connection = {name: fields.baseName.value, protocol: fields.protocol.value, baseUrl: fields.baseUrl.value, apiKey: fields.apiKey.value, model: fields.customModel.value};
      delete result.connectionId;
    }
    return result;
  };
  fields.roleId.onchange = () => { updatePrompt(); updateModel(); };
  fields.prompt.oninput = () => { promptOverride = true; updatePrompt(); };
  fields.connectionId.onchange = () => { chosenModel = ''; updateModel(); };
  fields.model.onchange = () => { chosenModel = fields.model.value; updateModel(); };
  fields.protocol.onchange = updateModel;
  fields.customModel.oninput = updateModel;
  form.querySelector('[data-inherit-prompt]').onclick = () => { promptOverride = false; updatePrompt(); };
  form.querySelector('[data-employee-back]').onclick = () => renderEmployeeManager(context);
  form.querySelector('[data-employee-share]').onclick = () => renderEmployeeShare(context, async item => {
    if (item) data.connections.push(item);
    title.textContent = employee ? '编辑员工配置' : '添加虚拟员工';
    host.replaceChildren(form);
    if (item) {
      fields.connectionId.add(new Option(item.name, item.id));
      fields.connectionId.value = item.id;
      updateModel();
    }
  });
  const check = form.querySelector('[data-test-connection]');
  check.onclick = async () => {
    const result = form.querySelector('[data-test-result]');
    if (fields.connectionId.value === 'custom' && !['baseName', 'baseUrl', 'customModel'].every(name => fields[name].reportValidity())) return;
    check.disabled = true;
    result.textContent = '正在从平台尝试调用所选模型…';
    try {
      await api(employeeProjectPath(projectId) + '/employee-model-connections/test', {method: 'POST', body: JSON.stringify(connectionPayload()), signal: context.signal});
      result.textContent = '连接成功，模型可以回复。';
    } catch (error) {
      if (!context.signal.aborted) result.textContent = error.message;
    } finally {
      check.disabled = false;
    }
  };
  form.onsubmit = async event => {
    event.preventDefault();
    const error = form.querySelector('.employee-form-error');
    const submit = form.querySelector('button[type="submit"]');
    if (dialog.employeeBusy) return;
    error.hidden = true;
    submit.disabled = dialog.employeeBusy = true;
    const close = dialog.querySelector('[data-employee-close]');
    close.disabled = true;
    const payload = {...connectionPayload(), roleId: fields.roleId.value, duty: fields.duty.value, prompt: promptOverride ? fields.prompt.value : null};
    if (employee) payload.revision = employee.revision;
    else Object.assign(payload, {displayName: fields.displayName.value, loginName: fields.loginName.value, password: fields.password.value});
    try {
      const base = employeeProjectPath(projectId) + '/test-employees';
      const saved = await api(employee ? base + '/' + employee.userId : base, {method: employee ? 'PUT' : 'POST', body: JSON.stringify(payload)});
      if (!employee) mergeEmployeeIntoProject(saved);
      toast(employee ? '员工配置已保存' : '员工已加入项目，可以用新账号登录', 'ok');
      dialog.close();
      if (!employee) {
        const row = $('#memberRows [data-member-user="' + Number(saved.employee.userId) + '"]');
        row?.scrollIntoView({block: 'nearest'});
        row?.querySelector('button')?.focus();
      }
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
    } finally {
      submit.disabled = dialog.employeeBusy = close.disabled = false;
    }
  };
  updatePrompt();
  updateModel();
  form.querySelector('input, select')?.focus();
}

async function renderEmployeeShare(context, done) {
  const {title, host, projectId, signal} = context;
  title.textContent = '授权现有模型连接';
  host.innerHTML = '<p class="employee-status" role="status">正在读取你的连接…</p>';
  try {
    const sources = await api('/api/employee-model-sources', {silent: true, signal});
    if (signal.aborted) return;
    if (!sources.length) {
      host.innerHTML = '<p class="employee-status">个人配置中暂无可以授权的 API 连接。</p><button class="button ghost" type="button" data-share-back>返回</button>';
      host.querySelector('[data-share-back]').onclick = () => done();
      return;
    }
    host.innerHTML = `<form class="employee-form">
      <p class="employee-status">所选连接将供本项目的员工调用。访问密钥仍由你的个人配置保管。</p>
      <label>个人连接<select name="source">${sources.map(item => employeeOption(item.source, item.name)).join('')}</select></label>
      <label>项目内显示名称<input name="name" maxlength="80" placeholder="例如 项目 Magpie"></label>
      <div data-share-models></div>
      <p class="employee-form-error" role="alert" hidden></p>
      <footer><button type="button" class="button ghost" data-share-back>返回</button><button type="submit" class="button primary">授权所选模型</button></footer></form>`;
    const form = host.querySelector('form');
    const models = form.querySelector('[data-share-models]');
    const update = () => {
      const source = sources.find(item => item.source === form.elements.source.value);
      models.innerHTML = source?.models?.length ? '<fieldset><legend>允许项目使用的模型</legend>' + source.models.map(id => `<label class="employee-inline"><input type="checkbox" name="models" value="${escapeHTML(id)}"> ${escapeHTML(id)}</label>`).join('') + '</fieldset>' :
        '<label>允许使用的模型 ID<input name="manualModels" placeholder="多个模型用英文逗号分隔" required></label>';
    };
    form.elements.source.onchange = update;
    update();
    form.querySelector('[data-share-back]').onclick = () => done();
    form.onsubmit = async event => {
      event.preventDefault();
      const data = new FormData(form);
      const selected = data.getAll('models');
      const allowed = selected.length ? selected : String(data.get('manualModels') || '').split(',').map(id => id.trim()).filter(Boolean);
      const error = form.querySelector('.employee-form-error');
      const submit = form.querySelector('button[type="submit"]');
      if (!allowed.length) {
        error.hidden = false;
        error.textContent = '请选择要授权给当前项目的模型。';
        return;
      }
      submit.disabled = true;
      try {
        const item = await api(employeeProjectPath(projectId) + '/employee-model-connections', {method: 'POST', body: JSON.stringify({source: data.get('source'), name: data.get('name'), models: allowed}), signal});
        title.textContent = '员工配置';
        await done(item);
      } catch (err) {
        if (!signal.aborted) { error.hidden = false; error.textContent = err.message; }
      } finally {
        submit.disabled = false;
      }
    };
  } catch (error) {
    if (!signal.aborted) host.textContent = error.message;
  }
}

function mergeEmployeeIntoProject(saved) {
  storeSavedProject(saved.project);
  if (saved.user && !userById(saved.user.id)) state.data.users.push(saved.user);
  if (state.projectDraftFor === saved.project.id && state.projectDraft && state.projectDraftBase) {
    const priorMembers = state.projectDraftBase.members || [];
    const newMember = saved.project.members.find(member => member.userId === saved.employee.userId);
    const roster = members => members.map(member => [Number(member.userId), member.role]).sort((a, b) => a[0] - b[0]);
    const expected = roster(priorMembers);
    const latest = roster(projectDraftFrom(saved.project).members.filter(member => member.userId !== Number(saved.employee.userId)));
    if (newMember && JSON.stringify(expected) === JSON.stringify(latest) && state.projectDraftBase.ownerId === saved.project.ownerId) {
      state.projectDraftBase.members = [...priorMembers, newMember];
      state.projectDraftBase.membersVersion = saved.project.membersVersion;
      state.projectDraft.members.push(newMember);
      state.projectDraft.membersVersion = saved.project.membersVersion;
    } else {
      toast('项目成员同时发生了其他变化，请重新核对项目成员名单', 'warn');
    }
    renderProjectDetail({force: true});
  }
}

async function employeeButtonRequest(button, operation) {
  button.disabled = true;
  try {
    await operation();
  } catch (error) {
    toast(error.message, 'err');
  } finally {
    button.disabled = false;
  }
}

async function openEmployeeConnectionSharing(projectId) {
  const dialog = getEmployeeDialog();
  if (!dialog.open) {
    dialog.employeeTrigger = document.activeElement;
    dialog.showModal();
  }
  dialog.employeeRequest?.abort();
  const request = dialog.employeeRequest = new AbortController();
  const host = dialog.querySelector('#employeeDialogContent');
  const title = dialog.querySelector('h2');
  const path = employeeProjectPath(projectId) + '/employee-model-connections';
  dialog.querySelector('#employeeDialogProject').textContent =
    (state.data.projects || []).find(project => project.id === projectId)?.name || projectId;
  title.textContent = '我的共享连接';
  host.innerHTML = '<p class="employee-status" role="status">正在读取你的连接…</p>';
  const context = {dialog, title, host, projectId, signal: request.signal};
  try {
    const items = await api(path, {silent: true, signal: request.signal});
    if (request.signal.aborted) return;
    const list = items.map(item => `<div class="employee-record"><div class="employee-record-text">
      <strong>${escapeHTML(item.name)}</strong><span>${escapeHTML(item.models.join('、'))}</span></div>
      <button type="button" class="button ghost small" data-share-revoke="${escapeHTML(item.id)}">撤销授权</button></div>`).join('');
    host.innerHTML = `<div class="employee-manager"><section><div class="employee-section-head">
      <h3>我的共享连接</h3><button type="button" class="button ghost small" data-employee-share>授权现有连接</button>
      </div>${list || '<p class="employee-status">暂无共享连接。</p>'}</section></div>`;
    host.querySelector('[data-employee-share]').onclick = () => renderEmployeeShare(context, () => openEmployeeConnectionSharing(projectId));
    for (const button of host.querySelectorAll('[data-share-revoke]')) {
      button.onclick = () => employeeButtonRequest(button, async () => {
        await api(path + '/' + encodeURIComponent(button.dataset.shareRevoke), {method: 'DELETE'});
        await openEmployeeConnectionSharing(projectId);
      });
    }
  } catch (error) {
    if (!request.signal.aborted) host.textContent = error.message;
  }
}
