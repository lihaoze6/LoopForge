import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../../web/app/rooms.js', import.meta.url), 'utf8');
const context = vm.createContext({
  state: {currentUser: {id: 20}},
  escapeHTML: text => text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
});
vm.runInContext(source, context);
const clone = value => JSON.parse(JSON.stringify(value));

test('draft edits preserve untouched mentions and remove text that no longer refers to the selected user', () => {
  const sameName = [{userId: 10, start: 0, end: 3}, {userId: 20, start: 4, end: 7}];
  assert.deepEqual(clone(context.reconcileRoomMentions('@小林 @小林', '好 @小林 @小林', sameName)),
    [{userId: 10, start: 2, end: 5}, {userId: 20, start: 6, end: 9}]);
  assert.deepEqual(clone(context.reconcileRoomMentions('@小林 @小林', '@小周 @小林', sameName)),
    [{userId: 20, start: 4, end: 7}]);
});

test('rendering highlights structured mentions by user id and escapes ordinary message content', () => {
  const markup = context.roomTextHTML('🙂 @小林 <tag> @小林', [{userId: 20, start: 2, end: 5}]);
  assert.equal(markup, '🙂 <span class="room-at me">@小林</span> &lt;tag&gt; @小林');
});

test('incoming timeline results refresh existing cards without dropping older messages', () => {
  const old = [
    {id: 'a', createdAt: '2026-01-01T00:00:00Z'},
    {id: 'b', createdAt: '2026-01-01T00:00:00.1Z', state: 'running'}
  ];
  const incoming = [
    {id: 'b', createdAt: '2026-01-01T00:00:00.1Z', state: 'finished'},
    {id: 'c', createdAt: '2026-01-01T00:00:00.11Z'}
  ];
  const merged = clone(context.mergeRoomMessages(old, incoming));
  assert.deepEqual(merged.map(message => message.id), ['a', 'b', 'c']);
  assert.equal(merged[1].state, 'finished');
});

test('room input limits count Unicode code points and keep issue text intact', () => {
  assert.equal(context.roomCodePointLength('🙂'.repeat(4000)), 4000);
  assert.equal(context.roomMessageLimitError('🙂'.repeat(4000)), '');
  assert.match(context.roomMessageLimitError('🙂'.repeat(4001)), /4000/);
  assert.equal(context.roomIssueLimitError('🙂'.repeat(200), 'a'.repeat(8000)), '');
  assert.match(context.roomIssueLimitError('🙂'.repeat(201), ''), /200/);
  assert.match(context.roomIssueLimitError('title', '🙂'.repeat(8001)), /8000/);
  assert.equal(context.splitRoomIssueText('🙂'.repeat(201) + '\nbody').title, '🙂'.repeat(201));
});

test('oversize chat and issue drafts are rejected before a network request', async t => {
  const {loadApp, settle} = await import('../lib/load-app.mjs');
  const app = await loadApp();
  t.after(app.close);
  const room = {id: 'room-1', projectId: 'project-1', name: 'Room', members: []};
  app.router.get(/^\/api\/rooms\?/, [room]);
  app.router.get('/api/rooms/room-1/messages', []);
  app.router.get('/api/rooms/room-1/discussion-settings', {enabled: false, replyLimit: 4});
  app.router.get(/^\/api\/room-mentions\?/, {mentions: [], unread: 0});
  app.evaluate(`state.projectFilter='project-1';switchView('rooms')`);
  await settle(6);

  app.evaluate(`
    state.rooms.mode = 'chat';
    $('#roomInput').value = '🙂'.repeat(4001);
    syncRoomDraft($('#roomInput').value);
    sendRoomMessage();
  `);
  await settle(3);
  assert.equal(app.router.calls.filter(call => call.path === '/api/rooms/room-1/messages' && call.method === 'POST').length, 0);
  assert.equal(context.roomCodePointLength(app.evaluate(`roomDraft().body`)), 4001);

  app.evaluate(`
    state.rooms.mode = 'issue';
    state.rooms.sops = ['flow-1'];
    $('#roomInput').value = '🙂'.repeat(201) + '\\nbody';
    syncRoomDraft($('#roomInput').value);
    dispatchRoomIssue();
  `);
  await settle(3);
  assert.equal(app.router.calls.filter(call => call.path === '/api/rooms/room-1/issues' && call.method === 'POST').length, 0);
  assert.equal(app.evaluate(`roomDraft().body`).startsWith('🙂'.repeat(201)), true);
});

test('the project editor submits only the employee and preserves unsaved project edits', async t => {
  const {loadApp, settle} = await import('../lib/load-app.mjs');
  const app = await loadApp();
  t.after(app.close);
  const initial = app.evaluate(`{
    const project = state.data.projects[0];
    state.currentUser = {...state.currentUser, id: 1, loginName: "owner", isGuest: false};
    project.ownerId = state.currentUser.id;
    project.members = [];
    project.membersVersion = 'first';
    project;
  }`);
  const projectId = initial.id;
  const path = '/api/projects/' + projectId + '/test-employees';
  app.router.get(path, {employees: [], roles: [], connections: [{id: 'magpie', name: 'Magpie', protocol: 'chat', models: ['demo-model']}]});
  const newMember = {userId: 99, role: 'member'};
  app.router.post(path, {
    project: {...initial, members: [newMember], membersVersion: 'second'},
    employee: {userId: 99}, user: {id: 99, loginName: 'lin.qa', chineseName: '小林'}
  });
  app.evaluate(`{
    state.projectAdminDetailId = '${projectId}';
    switchView('projects');
    state.projectAdminTab = 'members';
    state.projectDraft.name = '未保存的项目名';
    renderProjectDetail({force: true});
    document.querySelector('[data-test-employee-action="create"]').click();
  }`);
  await settle(8);
  const form = app.document.querySelector('.employee-dialog form');
  assert.ok(form);
  form.elements.displayName.value = '小林';
  form.elements.loginName.value = 'lin.qa';
  form.elements.password.value = 'test-password';
  form.elements.duty.value = '测试工程师';
  form.dispatchEvent(new app.window.Event('submit', {bubbles: true, cancelable: true}));
  await settle(8);
  const request = app.router.calls.find(call => call.path === path && call.method === 'POST');
  assert.equal(request.body.loginName, 'lin.qa');
  assert.equal(request.body.model, 'demo-model');
  assert.equal(request.body.connectionId, 'magpie');
  assert.equal(request.body.name, undefined);
  assert.equal(app.evaluate('state.projectDraft.name'), '未保存的项目名');
  assert.equal(app.evaluate('state.projectDraft.membersVersion'), 'second');
});
