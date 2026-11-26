let roomsPollTimer = 0;
const ROOM_MESSAGE_LIMIT = 4000;
const ROOM_ISSUE_TITLE_LIMIT = 200;
const ROOM_ISSUE_DESCRIPTION_LIMIT = 8000;

function roomProjectId() {
  return state.projectFilter || '';
}

function roomWorkflowChoices() {
  return (state.data?.workflows || []).filter(w => catalogGeneric(w)
    ? w.status !== 'draft'
    : w.status === 'published' && (catalogOwned(w.projectId) || catalogShared(w.projectId, w.shared)));
}

function roomWorkflowLabel(w) {
  return (w.name || w.id) + (catalogGeneric(w) ? '（通用）' : '');
}

function activeRoom() {
  const rs = roomState();
  return (rs.items || []).find(item => item.id === rs.activeId) || null;
}

function roomState() {
  if (!state.rooms) state.rooms = {items: [], activeId: '', sops: [], active: false, mode: 'chat'};
  if (!Array.isArray(state.rooms.sops)) state.rooms.sops = [];
  if (state.rooms.mode !== 'issue') state.rooms.mode = 'chat';
  return state.rooms;
}

function setRoomMode(mode) {
  roomState().mode = mode === 'issue' ? 'issue' : 'chat';
  syncRoomComposer();
  $('#roomInput')?.focus();
}

function roomRunTerminal(status) {
  return status === 'succeeded' || status === 'failed' || status === 'cancelled';
}

function bindRooms() {
  const create = $('#newRoom');
  const form = $('#roomComposer');
  if (!create || create.dataset.bound) return;
  create.dataset.bound = '1';
  create.onclick = createRoom;
  document.addEventListener('visibilitychange', () => {
    if (document.hidden || state.currentView !== 'rooms') return;
    refreshRoomMessages();
    refreshRoomInbox();
  });
  $('#renameRoom').onclick = renameRoom;
  $('#roomDetails').onclick = () => openRoomPanel('details');
  $('#roomInbox').onclick = () => openRoomPanel('inbox');
  $('#roomPanelClose').onclick = () => closeRoomPanel(true);
  $('#roomBack').onclick = () => {
    history.pushState(null, '', roomHash());
    closeRoomPanel();
    openRoom('');
  };
  $('#roomNewMessages').onclick = () => {
    if (roomState().anchored) {
      openRoom(roomState().activeId);
      return;
    }
    const log = $('#roomLog');
    log.scrollTop = log.scrollHeight;
    $('#roomNewMessages').hidden = true;
  };
  $('#roomPanel').addEventListener('keydown', event => {
    if (event.key === 'Escape') closeRoomPanel(true);
  });
  $('#roomModeChat').onclick = () => setRoomMode('chat');
  $('#roomModeIssue').onclick = () => setRoomMode('issue');
  form.onsubmit = event => {
    event.preventDefault();
    submitRoomComposer();
  };
  $('#roomDispatch').onclick = () => {
    if (roomTargetPicked()) dispatchRoomIssue();
    else openRoomMention({targetsOnly: true});
  };
  const input = $('#roomInput');
  input.addEventListener('keydown', event => {
    if (event.keyCode === 229 || event.isComposing) return;
    if (roomMentionKey(event)) return;
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      submitRoomComposer();
    }
  });
  input.addEventListener('input', event => {
    syncRoomDraft(input.value);
    syncRoomComposer();
    if (!event.isComposing) updateRoomMention();
  });
  input.addEventListener('click', updateRoomMention);
  document.addEventListener('mousedown', event => {
    if (!event.target.closest?.('.room-composer-box')) closeRoomMention();
  });
  $('#roomSOP').addEventListener('change', event => {
    const id = event.target.value;
    if (id) toggleRoomSOP(id, true);
    event.target.value = '';
  });

}

function toggleRoomSOP(id, force) {
  const rs = roomState();
  const has = rs.sops.includes(id);
  const want = force === undefined ? !has : force;
  if (want && !has) {
    if (rs.sops.length >= 5) return toast('一次最多派给 5 条 SOP');
    rs.sops.push(id);
  }
  if (!want && has) rs.sops = rs.sops.filter(item => item !== id);
  if ($('#roomSOPList')) fillRoomSOPList();
  else syncRoomComposer();
}

function roomTargetPicked() {
  return roomState().sops.length > 0;
}

function roomCodePointLength(value) {
  return Array.from(String(value || '')).length;
}

function roomMessageLimitError(body) {
  const length = roomCodePointLength(body);
  return length > ROOM_MESSAGE_LIMIT ? `消息不能超过 ${ROOM_MESSAGE_LIMIT} 个字符（当前 ${length}）` : '';
}

function roomIssueLimitError(title, description) {
  const titleLength = roomCodePointLength(title);
  const descriptionLength = roomCodePointLength(description);
  if (titleLength > ROOM_ISSUE_TITLE_LIMIT) return `Issue 标题不能超过 ${ROOM_ISSUE_TITLE_LIMIT} 个字符（当前 ${titleLength}）`;
  if (descriptionLength > ROOM_ISSUE_DESCRIPTION_LIMIT) return `Issue 描述不能超过 ${ROOM_ISSUE_DESCRIPTION_LIMIT} 个字符（当前 ${descriptionLength}）`;
  return '';
}

function syncRoomComposer() {
  const rs = roomState();
  const issue = rs.mode === 'issue';
  const names = new Map(roomWorkflowChoices().map(w => [w.id, roomWorkflowLabel(w)]));
  rs.sops = rs.sops.filter(id => names.has(id));
  const picks = $('#roomSOPPicks');
  if (picks) {
    picks.innerHTML = rs.sops.map(id => `<button type="button" class="room-pick" data-unpick="${escapeHTML(id)}" title="移除">${escapeHTML(names.get(id))}<i>×</i></button>`).join('');
    picks.querySelectorAll('[data-unpick]').forEach(button => { button.onclick = () => toggleRoomSOP(button.dataset.unpick, false); });
  }
  $('#roomSOP')?.closest('.room-chip')?.classList.toggle('on', rs.sops.length > 0);
  $('#roomComposer')?.classList.toggle('is-issue', issue);
  $('#roomModeChat')?.classList.toggle('on', !issue);
  $('#roomModeIssue')?.classList.toggle('on', issue);
  $('#roomModeChat')?.setAttribute('aria-pressed', String(!issue));
  $('#roomModeIssue')?.setAttribute('aria-pressed', String(issue));
  const input = $('#roomInput');
  if (input) input.placeholder = issue ? '第一行写 Issue 标题，下面写描述。输入 @ 选择 SOP' : '发消息，输入 @ 提醒同事';
  const picked = roomTargetPicked();
  const dispatch = $('#roomDispatch');
  if (dispatch) {
    dispatch.textContent = !picked ? '建单并派发' : rs.sops.length > 1 ? `建单并派发 ${rs.sops.length} 条 SOP` : '建单并派发';
    dispatch.title = picked ? '第一行作为 Issue 标题，其余作为描述' : '先选一条 SOP';
  }
  const hint = $('#roomTargetHint');
  if (hint) {
    hint.textContent = !picked
      ? '选择一条或多条 SOP。第一行是标题，回车建单。'
      : (rs.sops.length > 1 ? `回车将并行派给 ${rs.sops.length} 条 SOP。` : `回车将建单并派给 SOP「${names.get(rs.sops[0])}」。`);
  }
  $('#roomSend')?.classList.toggle('ready', !!input?.value.trim());
  if ($('#roomSend')) $('#roomSend').disabled = !input?.value.trim() || !!roomDraft().sending;
  document.querySelectorAll('[data-room-pick]').forEach(button => {
    button.classList.toggle('picked', rs.sops.includes(button.dataset.roomPick));
  });
}

function roomHash(id = '') {
  return id ? '#/rooms/' + encodeURIComponent(id) : '#/rooms';
}

function stopRoomView() {
  const rs = roomState();
  rs.listRequest?.abort();
  rs.threadRequest?.abort();
  clearTimeout(roomsPollTimer);
  closeRoomPanel();
  closeRoomMention();
}

function currentRoomProject() {
  return (state.data?.projects || []).find(item => item.id === roomProjectId());
}

function canManageRoomAutomation() {
  const id = Number(state.currentUser?.id);
  return id > 0 && (isPlatformAdmin() || Number(currentRoomProject()?.ownerId) === id);
}

function canWriteRoom() {
  const project = currentRoomProject();
  const id = Number(state.currentUser?.id);
  if (!project) return false;
  if (!state.authRequired || isPlatformAdmin() || Number(project.ownerId) === id) return true;
  if (!project.ownerId && !(project.members || []).length) return true;
  return (project.members || []).some(member => Number(member.userId) === id && member.role !== 'viewer');
}

async function loadRooms({route = false, projectChanged = false} = {}) {
  bindRooms();
  const rs = roomState();
  rs.listRequest?.abort();
  rs.threadRequest?.abort();
  clearTimeout(roomsPollTimer);
  const request = rs.listRequest = new AbortController();
  const options = {silent: true, signal: request.signal};
  if (projectChanged) history.replaceState(null, '', roomHash());
  if (projectChanged || rs.projectId !== roomProjectId()) {
    rs.items = [];
    renderRoomList([]);
    renderRoomThread(null, []);
    closeRoomPanel();
    $('#roomComposer').hidden = true;
    $('#roomLayout').classList.remove('has-room');
  }
  fillRoomTargets();
  let projectId = roomProjectId();
  const linked = route && routePart(0) === 'rooms' ? routePart(1) : '';
  try {
    if (linked && !rs.items.some(item => item.id === linked && item.projectId === projectId)) {
      const room = await api('/api/rooms/' + encodeURIComponent(linked), options);
      if (request.signal.aborted) return;
      projectId = room.projectId;
      state.projectFilter = projectId;
      localStorage.setItem('agentflow.activeProject', projectId);
      renderProjectSwitcher();
      fillRoomTargets();
    }
    rs.projectId = projectId;
    $('#newRoom').hidden = !canWriteRoom();
    $('#roomInbox').disabled = !projectId;
    if (!projectId) {
      rs.items = [];
      renderRoomList([]);
      await openRoom('');
      return;
    }
    const rooms = await api('/api/rooms?projectId=' + encodeURIComponent(projectId), options);
    if (request.signal.aborted) return;
    rs.items = rooms || [];
    if (linked && !rs.items.some(room => room.id === linked)) throw new Error('找不到项目协作群');
    const hasCurrent = rs.items.some(room => room.id === rs.activeId);
    const narrow = matchMedia('(max-width: 700px)').matches;
    const id = linked || (!narrow && (hasCurrent ? rs.activeId : rs.items[0]?.id)) || '';
    renderRoomList(rs.items);
    await openRoom(id, {show: !!linked || !narrow});
    if (!request.signal.aborted) refreshRoomInbox();
  } catch (error) {
    if (!request.signal.aborted) {
      rs.items = [];
      renderRoomList([]);
      await openRoom('');
      roomViewError(error.message);
    }
  }
}

function scheduleRoomPoll() {
  clearTimeout(roomsPollTimer);
  const rs = roomState();
  const delay = !rs.activeId ? 10000 : rs.active ? 4000 : 5000;
  roomsPollTimer = setTimeout(async () => {
    if (state.currentView !== 'rooms') return;
    if (!document.hidden) {
      if (rs.activeId) await refreshRoomMessages();
      await refreshRoomInbox();
    }
    if (state.currentView === 'rooms') scheduleRoomPoll();
  }, delay);
}

function fillRoomTargets() {
  const sop = $('#roomSOP');
  if (!sop) return;
  sop.innerHTML = '<option value="">添加 SOP…</option>' + roomWorkflowChoices().map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(roomWorkflowLabel(item))}</option>`).join('');
  syncRoomComposer();
}

function renderRoomList(rooms) {
  const host = $('#roomList');
  const count = $('#roomCount');
  if (count) count.textContent = String(rooms.length);
  if (!host) return;
  if (!rooms.length) {
    const note = roomProjectId() ? (canWriteRoom() ? '使用上方按钮创建协作群。' : '当前项目还没有协作群。') : '在顶栏选择项目后显示协作群。';
    host.innerHTML = `<div class="room-empty"><p>${escapeHTML(note)}</p></div>`;
    return;
  }
  host.innerHTML = rooms.map(room => `<button type="button" class="room-item ${room.id === roomState().activeId ? 'active' : ''}" data-room="${escapeHTML(room.id)}"><i>#</i><span data-i18n-skip>${escapeHTML(room.name)}</span><small title="群成员（即项目成员）">${(room.members || []).length} 人</small></button>`).join('');
  host.querySelectorAll('[data-room]').forEach(button => {
    button.onclick = () => selectRoom(button.dataset.room);
  });
}

function selectRoom(id) {
  if (location.hash !== roomHash(id)) history.pushState(null, '', roomHash(id));
  openRoom(id);
}

function roomViewError(message) {
  $('#roomLog').innerHTML = `<div class="room-empty-state"><h3>暂时无法读取协作群</h3><p>${escapeHTML(message)}</p><button type="button" class="button ghost small" data-room-retry>重试</button></div>`;
  $('#roomLog [data-room-retry]').onclick = () => loadRooms({route: true});
}

async function openRoom(id, {show = !!id} = {}) {
  const rs = roomState();
  const previous = rs.activeId;
  rs.threadRequest?.abort();
  clearTimeout(roomsPollTimer);
  const request = rs.threadRequest = new AbortController();
  rs.activeId = id || '';
  rs.anchored = false;
  rs.historyLoaded = false;
  rs.pendingNew = false;
  rs.messages = [];
  rs.hasMore = false;
  rs.threadSignature = '';
  $('#roomNewMessages').hidden = true;
  if (previous !== id) {
    rs.sops = [];
    rs.mode = 'chat';
    closeRoomMention();
    $('#roomInput').value = roomDraft().body;
  }
  $('#roomLayout').classList.toggle('has-room', show && !!id);
  renderRoomList(rs.items || []);
  const room = activeRoom();
  renderRoomResources(room);
  const writable = canWriteRoom();
  $('#roomTitle').toggleAttribute('data-i18n-skip', !!room);
  $('#roomTitle').textContent = room?.name || '选择一个群';
  $('#roomSubtitle').textContent = room ? lfT(`${(room.members || []).length} 位项目成员`, `${(room.members || []).length} project members`) : '';
  $('#roomDetails').hidden = !room;
  $('#renameRoom').hidden = !room || !writable;
  $('#roomComposer').hidden = !room || !writable;
  $('#roomReadonly').hidden = !room || writable;
  syncRoomComposer();
  if (!id) {
    renderRoomThread(null, []);
    if (roomProjectId()) scheduleRoomPoll();
    return;
  }
  $('#roomLog').innerHTML = '<div class="room-empty-state" role="status">正在读取消息…</div>';
  try {
    const list = await api('/api/rooms/' + encodeURIComponent(id) + '/messages', {silent: true, signal: request.signal});
    if (request.signal.aborted) return;
    rs.messages = list || [];
    rs.hasMore = rs.messages.length >= 200;
    renderRoomThread(room, rs.messages);
    if (!$('#roomPanel').hidden && roomState().panel === 'details') renderRoomSettings();
  } catch (error) {
    if (!request.signal.aborted) roomViewError(error.message);
  }
  if (!request.signal.aborted) scheduleRoomPoll();
}

function announceRoom(message) {
  $('#roomAnnouncement').textContent = message;
}

function roomAtBottom() {
  const log = $('#roomLog');
  return log.scrollHeight - log.scrollTop - log.clientHeight < 80;
}

async function refreshRoomMessages() {
  const rs = roomState();
  const id = rs.activeId;
  const request = rs.threadRequest;
  if (!id || rs.anchored || !request || request.signal.aborted || rs.polling === request) return;
  rs.polling = request;
  try {
    const messages = await api('/api/rooms/' + encodeURIComponent(id) + '/messages', {silent: true, signal: request.signal});
    if (request.signal.aborted || rs.anchored) return;
    const near = roomAtBottom();
    if (!rs.historyLoaded) rs.hasMore = messages.length >= 200;
    const next = rs.historyLoaded ? mergeRoomMessages(rs.messages, messages) : messages;
    const last = rs.messages.at(-1)?.id;
    if (!near && last && next.at(-1)?.id !== last) {
      rs.pendingNew = true;
      $('#roomNewMessages').textContent = '有新消息 ↓';
      $('#roomNewMessages').hidden = false;
    }
    rs.messages = next;
    renderRoomThread(activeRoom(), next, !near);
    announceRoom('');
  } catch (error) {
    if (!request.signal.aborted) {
      if (error.status === 403 || error.status === 404) {
        rs.messages = [];
        $('#roomComposer').hidden = true;
        closeRoomPanel();
        roomViewError(error.message);
      } else announceRoom('连接暂时中断，正在重试');
    }
  } finally {
    if (rs.polling === request) rs.polling = null;
  }
}

function mergeRoomMessages(previous, incoming) {
  const byId = new Map(previous.map(message => [message.id, message]));
  for (const message of incoming) byId.set(message.id, message);
  const stamp = value => String(value || '').replace(/(\.\d+)?Z$/, (_, fraction) => (fraction || '.').padEnd(10, '0') + 'Z');
  return [...byId.values()].sort((a, b) => stamp(a.createdAt).localeCompare(stamp(b.createdAt)) || String(a.id).localeCompare(b.id));
}

async function loadOlderRoomMessages() {
  const rs = roomState();
  const request = rs.threadRequest;
  const id = rs.activeId;
  const before = rs.messages[0]?.id;
  if (!id || !before || !request || rs.loadingHistory === request) return;
  rs.loadingHistory = request;
  const log = $('#roomLog');
  try {
    const page = await api('/api/rooms/' + encodeURIComponent(id) + '/history?before=' + encodeURIComponent(before), {silent: true, signal: request.signal});
    if (request.signal.aborted) return;
    const anchor = [...log.querySelectorAll('[data-room-message]')].find(el => el.offsetTop + el.offsetHeight > log.scrollTop);
    const offset = anchor ? anchor.offsetTop - log.scrollTop : 0;
    rs.hasMore = page.hasMore;
    rs.historyLoaded = true;
    rs.messages = mergeRoomMessages(page.messages, rs.messages);
    renderRoomThread(activeRoom(), rs.messages, true);
    if (anchor) {
      const current = log.querySelector('[data-room-message="' + CSS.escape(anchor.dataset.roomMessage) + '"]');
      if (current) log.scrollTop = current.offsetTop - offset;
    }
  } catch (error) {
    if (!request.signal.aborted) toast(error.message, 'err');
  } finally {
    if (rs.loadingHistory === request) rs.loadingHistory = null;
  }
}

function roomResGroup(title, count, body) {
  return `<section class="room-res-group"><div class="room-res-head"><span>${escapeHTML(title)}</span><em>${count}</em></div>${body || '<div class="room-res-more">暂无</div>'}</section>`;
}

const ROOM_MEMBER_ROLE_LABELS = {owner: '群主', maintainer: '维护者', member: '成员', viewer: '只读'};
function roomMemberRoleLabel(role) {
  const key = String(role || '').trim().toLowerCase();
  return ROOM_MEMBER_ROLE_LABELS[key] || role || '成员';
}
function roomResItem(icon, iconClass, name, sub, pick) {
  const tag = pick ? 'button' : 'div';
  const attrs = pick ? ` type="button" data-room-pick="${escapeHTML(pick)}" title="点击选为派发对象"` : '';
  return `<${tag} class="room-res-item ${pick ? '' : 'static'}"${attrs}><span class="room-res-icon ${iconClass}">${escapeHTML(icon)}</span><span class="room-res-text"><b>${escapeHTML(name)}</b>${sub ? `<small>${escapeHTML(sub)}</small>` : ''}</span></${tag}>`;
}

function renderRoomResources(room) {
  const host = $('#roomResources');
  if (!host) return;
  if (!roomProjectId()) {
    host.innerHTML = '<div class="room-empty"><b>先选择项目</b><p>选好项目后，这里会列出群成员，SOP 可以搜索后派发。</p></div>';
    return;
  }
  const members = room?.members || [];
  const workflows = roomWorkflowChoices();
  host.innerHTML = [
    roomResGroup('成员', members.length, members.length
      ? members.map(m => `<div class="room-res-item static"><span class="room-res-text"><b>${escapeHTML(m.name || ('用户 ' + m.userId))}<span class="room-member-tag">${escapeHTML(roomMemberRoleLabel(m.role))}</span></b></span></div>`).join('')
      : '<div class="room-res-more">这个项目还没有其他成员</div>'),
    roomResGroup('SOP', workflows.length, workflows.length
      ? `<input id="roomSOPSearch" class="room-sop-search" type="search" placeholder="搜索要派发的 SOP" value="${escapeHTML(roomState().sopQuery || '')}"><div id="roomSOPList"></div>`
      : roomNoSOPHint())
  ].join('');
  const search = $('#roomSOPSearch');
  if (search) {
    search.oninput = () => {
      roomState().sopQuery = search.value;
      fillRoomSOPList();
    };
  }
  fillRoomSOPList();
  bindRoomGoSOPs(host);
}

function fillRoomSOPList() {
  const list = $('#roomSOPList');
  if (!list) return;
  const rs = roomState();
  const query = String(rs.sopQuery || '').trim().toLowerCase();
  const picked = new Set(rs.sops);
  const all = roomWorkflowChoices();
  const rest = all.filter(w => !picked.has(w.id));
  const shown = query
    ? all.filter(w => roomWorkflowLabel(w).toLowerCase().includes(query) || String(w.id).toLowerCase().includes(query))
    : (rs.sopExpanded ? all : all.filter(w => picked.has(w.id)).concat(rest.slice(0, 4)));
  const hidden = query || rs.sopExpanded ? 0 : Math.max(0, rest.length - 4);
  const toggle = !query && rest.length > 4
    ? `<button type="button" class="room-res-more room-res-expand" data-room-sop-expand="${rs.sopExpanded ? 'collapse' : 'expand'}">${rs.sopExpanded ? '收起' : '展开其余 ' + hidden + ' 条'}</button>`
    : '';
  list.innerHTML = (shown.length
    ? shown.map(w => roomResItem('≡', 'sop', roomWorkflowLabel(w), (w.nodes || []).length ? (w.nodes.length + ' 个节点') : '点选后进入建单', w.id)).join('')
    : '<div class="room-res-more">没有匹配的 SOP</div>')
    + toggle;
  list.querySelectorAll('[data-room-pick]').forEach(button => {
    button.onclick = () => {
      setRoomMode('issue');
      toggleRoomSOP(button.dataset.roomPick);
    };
  });
  list.querySelector('[data-room-sop-expand]')?.addEventListener('click', event => {
    rs.sopExpanded = event.currentTarget.dataset.roomSopExpand === 'expand';
    fillRoomSOPList();
  });
  syncRoomComposer();
}

function renderRoomThread(room, messages, keepScroll) {
  const log = $('#roomLog');
  if (!log) return;
  const previous = log.scrollTop;
  const rs = roomState();
  rs.active = messages.some(m => m.kind === 'dispatch' && m.runId && !m.runFinal && !roomRunTerminal(m.runStatus || ''));
  // Polls usually return the same thread; skip the rebuild so open 产出摘要,
  // text selection and scroll position survive.
  const signature = (room?.id || '') + '|' + rs.hasMore + '|' + JSON.stringify(messages);
  if (signature === rs.threadSignature && log.childElementCount) return;
  rs.threadSignature = signature;
  if (!room) {
    log.innerHTML = `<div class="room-empty-state"><h3>${roomProjectId() ? '选择一个协作群' : '请先选择项目'}</h3><p>${roomProjectId() ? '从群列表打开聊天记录。' : '在顶栏选择项目，查看它的协作群。'}</p></div>`;
    return;
  }
  const mine = Number(state.currentUser?.id) || 0;
  let prevRun = null;
  const dispatchRuns = new Set(messages.filter(m => m.kind === 'dispatch' && m.runId).map(m => m.runId));
  const visible = messages.filter(message => {
    if (message.kind === 'progress' && message.nodeId && message.nodeStatus === 'succeeded') return false;
    if (message.kind === 'progress' && !message.nodeId && dispatchRuns.has(message.runId)) return false;
    return true;
  });
  const older = rs.hasMore ? '<div class="room-history-more"><button type="button" class="button ghost small" id="roomOlder">加载更早的消息</button></div>' : '';
  log.innerHTML = older + (visible.map(message => {
    const html = roomMessageHTML(message, mine, prevRun);
    if (['dispatch', 'progress', 'approval'].includes(message.kind)) prevRun = message;
    else if (message.kind !== 'decision') prevRun = null;
    return html;
  }).join('') || '<div class="room-empty-state"><h3>还没有消息</h3><p>给项目同事发一条消息，开始讨论。</p></div>');
  $('#roomOlder')?.addEventListener('click', loadOlderRoomMessages);
  bindRoomGoSOPs(log);
  log.querySelectorAll('[data-open-issue]').forEach(button => {
    button.onclick = () => openIssueDetail(button.dataset.openIssue);
  });
  log.querySelectorAll('[data-open-run]').forEach(button => {
    button.onclick = () => { location.hash = '#/runs/' + encodeURIComponent(button.dataset.openRun); };
  });
  log.querySelectorAll('[data-decide]').forEach(button => {
    button.disabled = !canWriteRoom();
    button.onclick = () => decideRoomStep(button.dataset.decide, button.dataset.run, button.dataset.node, button.dataset.name);
  });
  if (keepScroll) log.scrollTop = previous;
  else log.scrollTop = log.scrollHeight;
}

function roomRunStatusLabel(status) {
  return {running: '运行中', waiting: '等待处理', succeeded: '已完成', failed: '失败', cancelled: '已取消', queued: '排队中'}[status] || status || '排队中';
}

function roomIsSOPAuthor(name) {
  return /^SOP「.*」$/.test(String(name || ''));
}

function roomAuthorName(name) {
  return roomIsSOPAuthor(name) ? String(name).slice(4, -1) : String(name || '');
}

function roomAvatar(name) {
  if (roomIsSOPAuthor(name)) return '<span class="room-avatar sop">SOP</span>';
  return `<span class="room-avatar">${escapeHTML(String(name || '?').slice(0, 1))}</span>`;
}

function roomDispatchHTML(message, author) {
  const failed = message.dispatchError ? `<p class="room-card-error">派发未成功：${escapeHTML(message.dispatchError)}</p>` : '';
  const key = message.issueNumber ? '#' + message.issueNumber + ' ' : '';
  let run = '';
  if (message.runId) {
    const total = message.runTotal || 0;
    const done = message.runCompleted || 0;
    const pct = total ? Math.round(done * 100 / total) : 0;
    const status = message.runStatus || message.runFinal || 'queued';
    const current = (message.runCurrent || []).length ? '正在：' + message.runCurrent.join('、') : '';
    run = `<div class="room-run ${escapeHTML(status)}"><div class="room-run-head"><b>${escapeHTML(message.workflowName || 'SOP')}</b><span>${escapeHTML(roomRunStatusLabel(status))}${total ? ` · ${done}/${total} 步` : ''}</span></div><div class="room-run-track"><i style="width:${pct}%"></i></div>${current ? `<small>${escapeHTML(current)}</small>` : ''}</div>`;
  } else if (!message.dispatchError && !message.workflowId) {
    run = '<small class="room-card-note">当时只指定了角色，没有启动 SOP</small>';
  }
  const head = message.lane ? `${author} 并行派发` : `${author} 建了 Issue`;
  const actions = `<div class="room-card-actions"><button type="button" class="button ghost small" data-open-issue="${escapeHTML(message.issueId)}">打开 Issue</button>${message.runId ? `<button type="button" class="button ghost small" data-open-run="${escapeHTML(message.runId)}">查看运行</button>` : ''}</div>`;
  return `<article class="room-message room-card ${message.dispatchError ? 'failed' : ''}"><header>${head}</header><b>${escapeHTML(key + (message.issueTitle || 'Issue'))}</b>${message.lane ? '' : `<p>派给 ${escapeHTML(message.targetName || '未指定')}</p>`}${message.body ? `<p>${escapeHTML(message.body)}</p>` : ''}${failed}${run}${actions}</article>`;
}

function roomProgressHTML(message, prev) {
  const status = message.nodeStatus || '';
  const icon = status === 'succeeded' ? '✓' : status === 'failed' ? '✗' : status === 'cancelled' ? '■' : '•';
  const lines = String(message.body || '').split('\n');
  const head = lines.shift();
  const rest = lines.join('\n').trim();
  const detail = rest ? `<details><summary>产出摘要</summary><p>${escapeHTML(rest)}</p></details>` : '';
  const final = !message.nodeId;
  // Consecutive steps of one run already sit under its card; repeat the
  // issue / SOP only when the thread switches to another run or chat.
  const sameRun = prev && prev.runId && prev.runId === message.runId;
  const sop = roomIsSOPAuthor(message.author);
  const where = sameRun ? '' : [message.issueNumber ? '#' + message.issueNumber : '', sop ? '' : message.workflowName || ''].filter(Boolean).join(' · ');
  return `<article class="room-progress ${escapeHTML(status)} ${final ? 'final' : ''}">${roomAvatar(message.author)}<div><header><b>${escapeHTML(roomAuthorName(message.author) || 'SOP')}</b>${where ? `<span>${escapeHTML(where)}</span>` : ''}</header><p><i>${icon}</i>${escapeHTML(head)}</p>${detail}</div></article>`;
}

function roomApprovalHTML(message) {
  const attrs = `data-run="${escapeHTML(message.runId)}" data-node="${escapeHTML(message.nodeId)}" data-name="${escapeHTML(message.nodeName || message.nodeId)}"`;
  if (message.resolved) {
    return `<div class="room-decision">${escapeHTML(message.nodeName || '审批')} 已处理 · <button type="button" class="room-link" data-open-run="${escapeHTML(message.runId)}">看运行</button></div>`;
  }
  const actions = `<div class="room-card-actions"><button type="button" class="button primary small" data-decide="approve" ${attrs}>批准</button>${message.canReject ? `<button type="button" class="button ghost small" data-decide="reject" ${attrs}>打回</button>` : ''}<button type="button" class="button ghost small" data-open-run="${escapeHTML(message.runId)}">看运行</button></div>`;
  const where = [message.issueNumber ? '#' + message.issueNumber : '', message.issueTitle || ''].filter(Boolean).join(' ');
  return `<article class="room-message room-card room-approval ${message.resolved ? 'resolved' : ''}"><header>${escapeHTML(roomAuthorName(message.author) || 'SOP')} · ${escapeHTML(where)}</header><b>${message.resolved ? '✓' : '⏸'} ${escapeHTML(message.nodeName || '')} ${message.resolved ? '审批已处理' : '等待审批'}</b>${message.resolved ? '' : `<p>${escapeHTML(message.body || '')}</p>`}${actions}</article>`;
}

function roomMessageHTML(message, mine, prev) {
  const own = mine > 0 && Number(message.userId) === mine;
  const author = escapeHTML(message.author || '群成员');
  switch (message.kind) {
    case 'dispatch': return roomDispatchHTML(message, author);
    case 'progress': return roomProgressHTML(message, prev);
    case 'approval': return roomApprovalHTML(message);
    case 'decision': return `<div class="room-decision"><b>${author}</b> ${escapeHTML(message.body || '')}</div>`;
  }
  const user = userById(message.userId);
  const avatar = user ? userAvatarHTML(user, 32) : escapeHTML(Array.from(message.author || '群')[0]);
  const date = new Date(message.createdAt);
  const time = Number.isNaN(date.valueOf()) ? '' : new Intl.DateTimeFormat(document.documentElement.lang || 'zh-CN', {month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'}).format(date);
  return `<article class="room-message room-human-message ${own ? 'mine' : ''}" data-room-message="${escapeHTML(message.id)}" tabindex="-1">
    <span class="room-message-avatar" aria-hidden="true">${avatar}</span>
    <div><header><strong data-i18n-skip>${author}</strong> <time>${escapeHTML(time)}</time></header><p data-i18n-skip>${roomTextHTML(message.body || '', message.mentions || [])}</p></div>
  </article>`;
}

function roomTextHTML(text, mentions) {
  const characters = Array.from(text);
  const currentId = Number(state.currentUser?.id);
  let next = 0;
  let html = '';
  for (const mention of mentions) {
    html += escapeHTML(characters.slice(next, mention.start).join(''));
    const label = escapeHTML(characters.slice(mention.start, mention.end).join(''));
    html += `<span class="room-at${currentId === Number(mention.userId) ? ' me' : ''}">${label}</span>`;
    next = mention.end;
  }
  return html + escapeHTML(characters.slice(next).join(''));
}

function roomNoSOPHint() {
  return '<div class="room-res-more">当前项目还没有可派发的 SOP。<button type="button" class="room-link" data-room-go-sops>去「SOP 流程」新建或复制一条</button></div>';
}

function bindRoomGoSOPs(host) {
  host?.querySelectorAll('[data-room-go-sops]').forEach(button => {
    button.onclick = () => switchView('sops');
  });
}

function roomMentionState() {
  const rs = roomState();
  if (!rs.mention) rs.mention = {open: false, items: [], index: 0, start: -1, targetsOnly: false};
  return rs.mention;
}

function roomMentionToken(input) {
  const before = input.value.slice(0, input.selectionStart);
  const match = before.match(/(^|\s)@([^\s@]{0,30})$/);
  return match ? {start: before.length - match[2].length - 1, query: match[2]} : null;
}

function roomMentionItems(query, targetsOnly) {
  const q = String(query || '').trim().toLowerCase();
  const hit = (...values) => !q || values.some(value => String(value || '').toLowerCase().includes(q));
  const items = [];
  const wantSOP = targetsOnly || roomState().mode === 'issue';
  if (wantSOP) {
    roomWorkflowChoices().forEach(w => {
      if (hit(w.name, w.id)) items.push({kind: 'sop', id: w.id, name: roomWorkflowLabel(w), sub: (w.nodes || []).length ? w.nodes.length + ' 个步骤，派发后立即运行' : '派发后立即运行'});
    });
  }
  if (!targetsOnly && !wantSOP) {
    (activeRoom()?.members || []).forEach(m => {
      const user = userById(m.userId);
      const name = user?.chineseName || m.displayName || m.name || '用户 ' + m.userId;
      const login = user?.loginName || m.loginName || '';
      if (hit(name, login, m.name)) items.push({kind: 'member', id: String(m.userId), name, sub: [login, roomMemberRoleLabel(m.role)].filter(Boolean).join(' · ')});

    });
  }
  return items.slice(0, 40);
}

function openRoomMention({targetsOnly = false, token = null} = {}) {
  const ms = roomMentionState();
  ms.open = true;
  ms.targetsOnly = targetsOnly;
  ms.start = token ? token.start : -1;
  ms.items = roomMentionItems(token?.query || '', targetsOnly);
  ms.index = 0;
  renderRoomMention(token?.query || '');
}

function closeRoomMention() {
  const ms = roomMentionState();
  ms.targetsOnly = false;
  if (!ms.open) return;
  ms.open = false;
  const box = $('#roomMention');
  if (box) box.hidden = true;
  $('#roomInput')?.setAttribute('aria-expanded', 'false');
  $('#roomInput')?.removeAttribute('aria-activedescendant');
}

function updateRoomMention() {
  const input = $('#roomInput');
  if (!input) return;
  const token = roomMentionToken(input);
  if (token && (!token.query || roomMentionItems(token.query, false).length)) openRoomMention({token});
  else if (!roomMentionState().targetsOnly) closeRoomMention();
}

function renderRoomMention(query) {
  const box = $('#roomMention');
  const ms = roomMentionState();
  if (!box) return;
  const groups = [['sop', 'SOP · 建单并运行'], ['member', '成员 · 提醒']];
  let html = ms.targetsOnly ? '<div class="room-mention-title">选这条 Issue 要跑的 SOP</div>' : '';
  groups.forEach(([kind, label]) => {
    const rows = ms.items.map((item, index) => ({item, index})).filter(row => row.item.kind === kind);
    if (!rows.length && !(kind === 'sop' && !query && !roomWorkflowChoices().length)) return;
    html += `<div class="room-mention-group">${escapeHTML(label)}</div>`;
    if (!rows.length) html += roomNoSOPHint();
    html += rows.map(({item, index}) => `<button type="button" class="room-mention-item ${index === ms.index ? 'active' : ''}" data-mention="${index}" id="room-member-suggestion-${index}" role="option" aria-selected="${index === ms.index}"><span class="room-res-icon ${item.kind === 'member' ? '' : item.kind}">${escapeHTML(item.kind === 'sop' ? '≡' : (item.name || '?').slice(0, 1))}</span><span><b>${escapeHTML(item.name)}</b><small>${escapeHTML(item.sub || '')}</small></span></button>`).join('');
  });
  if (!ms.items.length && !html.includes('room-mention-group')) html += `<div class="room-mention-empty">没有匹配「${escapeHTML(query)}」的${roomState().mode === 'issue' || ms.targetsOnly ? ' SOP 或成员' : '成员'}</div>`;
  box.innerHTML = html;
  box.hidden = false;
  const input = $('#roomInput');
  input.setAttribute('aria-expanded', 'true');
  if (ms.items.length) input.setAttribute('aria-activedescendant', 'room-member-suggestion-' + ms.index);
  else input.removeAttribute('aria-activedescendant');
  box.querySelectorAll('[data-mention]').forEach(button => {
    button.onmousedown = event => event.preventDefault();
    button.onclick = () => pickRoomMention(ms.items[Number(button.dataset.mention)]);
  });
  bindRoomGoSOPs(box);
  box.querySelector('.room-mention-item.active')?.scrollIntoView({block: 'nearest'});
}

function roomMentionKey(event) {
  const ms = roomMentionState();
  if (!ms.open || event.isComposing) return false;
  if (event.key === 'Escape') {
    event.preventDefault();
    ms.targetsOnly = false;
    closeRoomMention();
    return true;
  }
  if (!ms.items.length) return false;
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    ms.index = (ms.index + (event.key === 'ArrowDown' ? 1 : ms.items.length - 1)) % ms.items.length;
    renderRoomMention(roomMentionToken($('#roomInput'))?.query || '');
    return true;
  }
  if (event.key === 'Tab') { closeRoomMention(); return false; }
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    pickRoomMention(ms.items[ms.index]);
    return true;
  }
  return false;
}

function pickRoomMention(item) {
  const input = $('#roomInput');
  const ms = roomMentionState();
  if (!item || !input) return;
  if (ms.start >= 0) {
    const caret = input.selectionStart;
    const insert = item.kind === 'member' ? '@' + item.name + ' ' : '';
    input.value = input.value.slice(0, ms.start) + insert + input.value.slice(caret).replace(/^ /, insert ? '' : '');
    syncRoomDraft(input.value);
    if (item.kind === 'member') {
      const draft = roomDraft();
      draft.mentions.push({userId: Number(item.id), start: ms.start, end: ms.start + insert.length - 1});
      draft.mentions.sort((a, b) => a.start - b.start);
    }
    const next = ms.start + insert.length;
    input.setSelectionRange(next, next);
  }
  if (item.kind === 'sop') {
    roomState().mode = 'issue';
    toggleRoomSOP(item.id, true);
  }
  ms.targetsOnly = false;
  closeRoomMention();
  syncRoomComposer();
  input.focus();
  if (item.kind === 'sop' && !input.value.trim()) toast('已选好 SOP，第一行写 Issue 标题，回车建单');
}

async function decideRoomStep(action, runId, nodeId, name) {
  const id = roomState().activeId;
  if (!id) return;
  let reason = '';
  if (action === 'reject') {
    reason = await afPrompt({title: '打回「' + name + '」', message: '说明要改什么，会带给被打回的步骤。', label: '打回原因', placeholder: '例如：边界情况没覆盖', confirmText: '打回'});
    if (reason == null) return;
  }
  try {
    await api('/api/rooms/' + encodeURIComponent(id) + '/decisions', {method: 'POST', body: JSON.stringify({runId, nodeId, action, reason: String(reason || '').trim()})});
    toast(action === 'approve' ? '已批准' : '已打回');
    await refreshRoomMessages();
  } catch (error) {
    toast(error.message);
  }
}

async function renameRoom() {
  const room = activeRoom();
  if (!room) return toast('先选择一个群');
  const name = await afPrompt({title: '重命名协作群', message: '新名称会显示在左侧群列表和顶栏。', label: '群名称', placeholder: '例如：需求值班群', defaultValue: room.name, confirmText: '保存'});
  if (name == null) return;
  const next = String(name).trim();
  if (!next || next === room.name) return;
  try {
    const updated = await api('/api/rooms/' + encodeURIComponent(room.id), {method: 'PATCH', body: JSON.stringify({name: next})});
    room.name = updated.name || next;
    renderRoomList(roomState().items || []);
    const title = $('#roomTitle');
    if (title) title.textContent = room.name;
    toast('已重命名');
  } catch (error) {
    toast(error.message);
  }
}

async function createRoom() {
  const projectId = roomProjectId();
  if (!projectId) return toast('先在顶栏选择项目');
  const name = await afPrompt({title: '新建协作群', message: '群名称会显示在当前项目的群列表里。', label: '群名称', placeholder: '例如：需求值班群', confirmText: '创建'});
  if (!name || !String(name).trim()) return;
  try {
    const room = await api('/api/rooms', {method: 'POST', body: JSON.stringify({projectId, name: String(name).trim()})});
    roomState().items.unshift(room);
    history.pushState(null, '', roomHash(room.id));
    await loadRooms({route: true});
    toast('已创建协作群');
  } catch (error) {
    toast(error.message);
  }
}

function submitRoomComposer() {
  if (roomState().mode === 'issue') dispatchRoomIssue();
  else sendRoomMessage();
}

function roomDraft(id = roomState().activeId) {
  const rs = roomState();
  rs.drafts ||= new Map();
  if (!rs.drafts.has(id)) rs.drafts.set(id, {body: '', mentions: [], clientId: ''});
  return rs.drafts.get(id);
}

function reconcileRoomMentions(before, after, mentions) {
  let start = 0;
  while (start < before.length && start < after.length && before[start] === after[start]) start++;
  let tail = 0;
  while (tail < before.length - start && tail < after.length - start && before[before.length - 1 - tail] === after[after.length - 1 - tail]) tail++;
  const end = before.length - tail;
  const delta = after.length - before.length;
  return mentions.flatMap(mention => {
    if (mention.end <= start) return [mention];
    if (mention.start >= end) return [{...mention, start: mention.start + delta, end: mention.end + delta}];
    return [];
  });
}

function syncRoomDraft(value) {
  const draft = roomDraft();
  if (draft.body === value) return;
  draft.mentions = reconcileRoomMentions(draft.body, value, draft.mentions);
  draft.body = value;
  draft.clientId = '';
}

async function sendRoomMessage() {
  const id = roomState().activeId;
  if (!id || !canWriteRoom()) return;
  const input = $('#roomInput');
  const button = $('#roomSend');
  syncRoomDraft(input.value);
  const draft = roomDraft(id);
  if (draft.sending || !draft.body.trim()) return;
  const body = draft.body;
  const limitError = roomMessageLimitError(body);
  if (limitError) return toast(limitError, 'err');
  const mentions = draft.mentions.map(mention => ({
    userId: mention.userId,
    start: Array.from(body.slice(0, mention.start)).length,
    end: Array.from(body.slice(0, mention.end)).length
  }));
  draft.clientId ||= globalThis.crypto?.randomUUID?.() || (Date.now().toString(36) + '-' + Math.random().toString(36).slice(2));
  const clientId = draft.clientId;
  draft.sending = true;
  button.disabled = true;
  try {
    await api('/api/rooms/' + encodeURIComponent(id) + '/messages', {method: 'POST', body: JSON.stringify({body, mentions, clientId})});
    if (draft.clientId === clientId) {
      draft.body = '';
      draft.mentions = [];
      draft.clientId = '';
      if (roomState().activeId === id) input.value = '';
    }
    if (state.currentView === 'rooms' && roomState().activeId === id) {
      closeRoomMention();
      await refreshRoomMessages();
      syncRoomComposer();
    }
  } catch (error) {
    toast(error.message, 'err');
  } finally {
    draft.sending = false;
    if (roomState().activeId === id) syncRoomComposer();
  }
}

function splitRoomIssueText(text) {
  const lines = String(text || '').split('\n');
  const title = (lines.shift() || '').trim();
  return {title, description: lines.join('\n').trim()};
}

async function dispatchRoomIssue() {
  const rs = roomState();
  const id = rs.activeId;
  if (!id) return toast('先选择一个群');
  const {title, description} = splitRoomIssueText($('#roomInput')?.value);
  const workflowIds = rs.sops.slice();
  if (!title) return toast('第一行写 Issue 标题');
  const limitError = roomIssueLimitError(title, description);
  if (limitError) return toast(limitError, 'err');
  if (!workflowIds.length) return toast('请选择要派发的 SOP');
  const button = $('#roomDispatch');
  button.disabled = true;
  try {
    const result = await api('/api/rooms/' + encodeURIComponent(id) + '/issues', {method: 'POST', body: JSON.stringify({title, description, workflowIds})});
    if (state.currentView !== 'rooms' || rs.activeId !== id) return;
    $('#roomInput').value = '';
    syncRoomDraft('');
    rs.sops = [];
    rs.mode = 'chat';
    closeRoomMention();
    syncRoomComposer();
    if (result.issue) {
      const items = state.data.issues || [];
      const index = items.findIndex(issue => issue.id === result.issue.id);
      if (index >= 0) items[index] = result.issue;
      else items.unshift(result.issue);
      state.data.issues = items;
    }
    await openRoom(id);
    const started = (result.tasks || []).length;
    toast(result.dispatchError ? 'Issue 已创建，部分派发未成功：' + result.dispatchError : (started ? `已建单并启动 ${started} 条 SOP，进度会显示在卡片上` : 'Issue 已创建，但没有启动 SOP'));
  } catch (error) {
    toast(error.message);
  } finally {
    button.disabled = false;
  }
}


function closeRoomPanel(restoreFocus = false) {
  const panel = $('#roomPanel');
  if (!panel) return;
  panel.hidden = true;
  $('#roomLayout').classList.remove('has-panel');
  $('#roomDetails').setAttribute('aria-expanded', 'false');
  if (restoreFocus) (roomState().panel === 'inbox' ? $('#roomInbox') : $('#roomDetails')).focus();
}

function openRoomPanel(mode) {
  roomState().panel = mode;
  $('#roomPanel').hidden = false;
  $('#roomLayout').classList.add('has-panel');
  $('#roomPanelTitle').textContent = mode === 'inbox' ? '提到我' : '成员与设置';
  $('#roomInboxList').hidden = mode !== 'inbox';
  $('#roomPanelDetails').hidden = mode === 'inbox';
  $('#roomDetails').setAttribute('aria-expanded', String(mode === 'details'));
  $('#roomPanelClose').focus();
  if (mode === 'inbox') refreshRoomInbox();
  else renderRoomSettings();
}

async function refreshRoomInbox() {
  const rs = roomState();
  const project = roomProjectId();
  const request = rs.listRequest;
  if (!project || !request || request.signal.aborted) return;
  try {
    const data = await api('/api/room-mentions?projectId=' + encodeURIComponent(project), {silent: true, signal: request.signal});
    if (request.signal.aborted || roomProjectId() !== project) return;
    const badge = $('#roomInboxCount');
    badge.hidden = !data.unread;
    badge.textContent = data.unread > 99 ? '99+' : String(data.unread || '');
    const list = $('#roomInboxList');
    const signature = JSON.stringify(data.mentions);
    if (list.dataset.signature === signature) return;
    list.dataset.signature = signature;
    list.innerHTML = (data.mentions || []).map((notice, index) => `<button type="button" class="room-inbox-item" data-room-notice="${index}"><span><b>${escapeHTML(notice.roomName)}</b>${notice.read ? '' : '<span class="room-unread-dot" aria-label="未读"></span>'}</span><strong data-i18n-skip>${escapeHTML(notice.message.author || '群成员')}</strong><small data-i18n-skip>${escapeHTML(notice.message.body || '')}</small></button>`).join('') ||
      '<p class="room-panel-empty">还没有人提到你。</p>';
    list.querySelectorAll('[data-room-notice]').forEach(button => {
      button.onclick = () => openRoomNotice(data.mentions[Number(button.dataset.roomNotice)]);
    });
  } catch (error) {
    if (!request.signal.aborted && rs.panel === 'inbox') {
      $('#roomInboxList').textContent = error.message;
      delete $('#roomInboxList').dataset.signature;
    }
  }
}

async function openRoomNotice(notice) {
  const id = notice.roomId;
  const messageId = notice.message.id;
  if (!roomState().items.some(room => room.id === id)) return;
  if (location.hash !== roomHash(id)) history.pushState(null, '', roomHash(id));
  await openRoom(id);
  const rs = roomState();
  const request = rs.threadRequest;
  if (rs.activeId !== id || request.signal.aborted) return;
  const path = '/api/rooms/' + encodeURIComponent(id);
  const options = {silent: true, signal: request.signal};
  const selector = '[data-room-message="' + CSS.escape(messageId) + '"]';
  try {
    if (!$(selector)) {
      rs.anchored = true;
      const [item, page] = await Promise.all([
        api(path + '/messages/' + encodeURIComponent(messageId), options),
        api(path + '/history?before=' + encodeURIComponent(messageId), options)
      ]);
      if (request.signal.aborted) return;
      rs.messages = [...page.messages, item];
      rs.hasMore = page.hasMore;
      renderRoomThread(activeRoom(), rs.messages, true);
      $('#roomNewMessages').textContent = '回到最新消息 ↓';
      $('#roomNewMessages').hidden = false;
    }
    closeRoomPanel();
    const item = $(selector);
    item?.classList.add('is-highlighted');
    item?.scrollIntoView({block: 'center'});
    item?.focus({preventScroll: true});
    await api(path + '/mentions/' + encodeURIComponent(messageId) + '/read', {...options, method: 'PUT'});
    await refreshRoomInbox();
  } catch (error) {
    if (!request.signal.aborted) toast(error.message, 'err');
  }
}

async function renderRoomSettings() {
  const room = activeRoom();
  const el = $('#roomSettings');
  el.replaceChildren();
  if (!room) return;
  const signal = roomState().threadRequest?.signal;
  const path = '/api/rooms/' + encodeURIComponent(room.id) + '/discussion-settings';
  const projectPath = '/api/projects/' + encodeURIComponent(room.projectId);
  const userId = Number(state.currentUser?.id);
  const selfPath = projectPath + '/test-employees/' + userId + '/pause';
  try {
    const [settings, self] = await Promise.all([
      api(path, {silent: true, signal}),
      userId > 0 ? api(selfPath, {silent: true, signal}).catch(() => null) : null
    ]);
    if (signal?.aborted || activeRoom()?.id !== room.id) return;
    const section = document.createElement('section');
    section.className = 'room-settings-section';
    section.innerHTML = '<h4>自动讨论</h4><p class="room-settings-summary"></p>';
    section.querySelector('p').textContent = settings.enabled ? lfT(`已启用 · 每轮最多 ${settings.replyLimit} 条自动回复`, `Enabled · up to ${settings.replyLimit} replies per round`) : '已关闭';
    el.append(section);
    const project = currentRoomProject();
    const hasMembership = userId > 0 && (canManageRoomAutomation() || (project?.members || []).some(member => Number(member.userId) === userId));
    if (hasMembership) {
      const sharing = document.createElement('button');
      sharing.type = 'button';
      sharing.className = 'button ghost small';
      sharing.textContent = '我的共享连接';
      sharing.onclick = () => openEmployeeConnectionSharing(room.projectId);
      el.append(sharing);
    }
    if (canManageRoomAutomation()) {
      const form = document.createElement('form');
      form.className = 'room-settings-form';
      form.innerHTML = `<label class="room-switch"><input name="enabled" type="checkbox" role="switch"${settings.enabled ? ' checked' : ''}> 在此群启用</label>
        <label>连续回复上限<input name="replyLimit" class="input" type="number" min="1" max="12" value="${Number(settings.replyLimit) || 4}" required></label>
        <p class="room-field-note">启用后，已配置的员工模型可读取本群近期讨论。</p>
        <button type="submit" class="button primary small">保存群设置</button>`;
      form.onsubmit = async event => {
        event.preventDefault();
        const button = form.querySelector('button');
        button.disabled = true;
        try {
          await api(path, {method: 'PUT', body: JSON.stringify({enabled: form.elements.enabled.checked, replyLimit: Number(form.elements.replyLimit.value)})});
          toast('群设置已保存', 'ok');
          if (activeRoom()?.id === room.id) renderRoomSettings();
        } catch (error) {
          toast(error.message, 'err');
        } finally {
          button.disabled = false;
        }
      };
      section.append(form);
    }
    if (self) {
      const handover = document.createElement('button');
      handover.type = 'button';
      handover.className = 'button ghost small';
      handover.textContent = self.paused ? '恢复我的自动参与' : '暂停我的自动参与';
      handover.onclick = async () => {
        handover.disabled = true;
        try {
          await api(selfPath, {method: 'PUT', body: JSON.stringify({paused: !self.paused})});
          await renderRoomSettings();
        } catch (error) {
          toast(error.message, 'err');
        } finally {
          handover.disabled = false;
        }
      };
      section.append(handover);
    }
  } catch (error) {
    if (!signal?.aborted) el.textContent = error.message;
  }
}
