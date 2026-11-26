// Collaboration rooms and group-mode run detail, without a model.
import test from 'node:test';
import assert from 'node:assert/strict';
import { loadApp, settle } from '../lib/load-app.mjs';

test('creating a collaboration room uses the existing prompt and posts the room', async (t) => {
  const app = await loadApp();
  t.after(() => {
    app.evaluate('clearTimeout(roomsPollTimer)');
    return app.close();
  });
  app.router.post('/api/rooms', (call) => ({
    id: 'room-1', name: call.body.name, projectId: call.body.projectId, members: [],
  }));
  app.router.get(/^\/api\/rooms\?/, [{
    id: 'room-1', name: '需求值班群', projectId: 'project-1', members: [],
  }]);
  app.router.get('/api/rooms/room-1', {id: 'room-1', name: '需求值班群', projectId: 'project-1', members: []});
  app.router.get(/^\/api\/rooms\/room-1\/messages/, []);
  app.router.get(/^\/api\/room-mentions\?/, {mentions: [], unread: 0});
  app.evaluate(`state.projectFilter='project-1';switchView('rooms')`);
  await settle(6);

  app.document.querySelector('#newRoom').click();
  await settle(4);
  assert.equal(app.document.querySelector('#confirmTitle').textContent, '新建协作群');
  assert.equal(app.document.querySelector('#confirmMessage').textContent, '群名称会显示在当前项目的群列表里。');
  assert.equal(app.document.querySelector('#confirmPromptLabel').textContent, '群名称');
  assert.equal(app.document.querySelector('#confirmPromptInput').placeholder, '例如：需求值班群');
  assert.equal(app.document.querySelector('#confirmOkBtn').textContent, '创建');

  app.document.querySelector('#confirmPromptInput').value = '  需求值班群  ';
  app.document.querySelector('#confirmOkBtn').click();
  await settle(8);

  const created = app.router.calls.filter((call) => call.method === 'POST' && call.path === '/api/rooms');
  assert.equal(created.length, 1);
  assert.deepEqual(created[0].body, { projectId: 'project-1', name: '需求值班群' });
  assert.match(app.document.body.textContent, /已创建协作群/);
});

test('a group-mode run detail shows the existing collaboration copy', async (t) => {
  const app = await loadApp();
  t.after(app.close);
  const workflow = app.evaluate('structuredClone(state.data.workflows[0])');
  workflow.collaborationMode = 'group';
  workflow.version = 3;
  workflow.nodes = [
    { id: 'node-1', type: 'agent', name: 'Implement' },
    { id: 'task-1', type: 'agent', name: 'Room task', requestId: 'req-1' },
  ];
  const run = {
    ...app.evaluate('structuredClone(state.data.runs[0])'),
    status: 'succeeded',
    kind: 'sop',
    finishedAt: '2026-10-21T10:05:00Z',
    workflowSnapshot: workflow,
    nodes: { 'node-1': { status: 'succeeded', attempt: 1 } },
  };
  app.router.get('/api/runs/run-1', run);
  app.evaluate(`state.data.runs=[${JSON.stringify(run)}];state.selectedRun='run-1';state.activeRun=state.data.runs[0]`);
  app.evaluate(`switchView('runs');showRunDetail('run-1')`);
  await settle(6);
  app.evaluate('renderRunDetail(state.activeRun)');

  const detail = app.document.querySelector('#runDetail').textContent;
  assert.match(detail, /协作模式/);
  assert.match(detail, /历史协作记录/);
  assert.match(detail, /SOP 主干已冷冻/);
  assert.match(detail, /1 步可展开/);
  assert.match(detail, /群内任务 1 个/);
  assert.match(detail, /冷冻 SOP v3/);
  assert.doesNotMatch(app.evaluate(`document.querySelector('#runDetail .run-progress-cell span').textContent`), /执行进度/);
});
