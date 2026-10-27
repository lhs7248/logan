import reducer from './reducer';
import {fetchPageInitData, updateFilterConditions, updateSorted, initState} from './action';
import {fetchWebTaskInfoByTaskIdApi, fetchWebBriefsApi, fetchWebTaskDetailsByDetailIdsApi} from '../../../common/api';

jest.mock('../../../common/api', () => ({
  fetchWebTaskInfoByTaskIdApi: jest.fn(),
  fetchWebBriefsApi: jest.fn(),
  fetchWebTaskDetailsByDetailIdsApi: jest.fn(),
  fetchWebSingleLogDetailByLogIdApi: jest.fn()
}));

let state;
const dispatch = action => { state = reducer(state, action); };
const run = action => action(dispatch, () => ({webLogDetail: state}));
const briefs = [
  {detailId: 11, logType: 1, logTime: 100},
  {detailId: 22, logType: 2, logTime: 200},
  {detailId: 33, logType: 1, logTime: 300}
];
const filters = {logTypes: [1, 2], keyword: 'shared'};

beforeEach(() => {
  jest.clearAllMocks();
  state = reducer(undefined, {});
  // Metadata describes only the first task and must not narrow the query scope.
  fetchWebTaskInfoByTaskIdApi.mockResolvedValue({taskId: 7, tasks: null});
  fetchWebBriefsApi.mockResolvedValue([briefs]);
  fetchWebTaskDetailsByDetailIdsApi.mockImplementation(ids => Promise.resolve(
    ids.split(',').filter(Boolean).map(id => ({id: Number(id)}))
  ));
});

it('keeps all requested tasks when filtering by keyword and log type', async () => {
  await run(fetchPageInitData('7,8,9'));
  await run(updateFilterConditions(filters));
  expect(fetchWebBriefsApi).toHaveBeenLastCalledWith('7,8,9', '1,2', 'shared');
  expect(state.taskDetails.map(x => x.id)).toEqual([11, 22, 33]);
});

it('keeps all tasks in both sort directions', async () => {
  await run(fetchPageInitData('7,8,9'));
  await run(updateSorted(false));
  expect(fetchWebBriefsApi).toHaveBeenLastCalledWith('7,8,9', '1,2', '');
  expect(state.taskDetails.map(x => x.id)).toEqual([33, 22, 11]);
  await run(updateSorted(true));
  expect(fetchWebBriefsApi).toHaveBeenLastCalledWith('7,8,9', '1,2', '');
  expect(state.taskDetails.map(x => x.id)).toEqual([11, 22, 33]);
});

it('preserves descending order when filters change', async () => {
  await run(fetchPageInitData('7,8,9'));
  await run(updateSorted(false));
  await run(updateFilterConditions(filters));
  expect(state.taskDetails.map(x => x.id)).toEqual([33, 22, 11]);
});

it('handles empty filtered results and sorting without requesting empty detail IDs', async () => {
  await run(fetchPageInitData('7,8,9'));
  fetchWebBriefsApi.mockResolvedValue([]);
  fetchWebTaskDetailsByDetailIdsApi.mockClear();
  await run(updateFilterConditions(filters));
  await run(updateSorted(false));
  expect(state.briefs).toEqual([]);
  expect(state.taskDetails).toEqual([]);
  expect(fetchWebTaskDetailsByDetailIdsApi).not.toHaveBeenCalled();
});

it('handles an initially empty task without requesting empty detail IDs', async () => {
  fetchWebBriefsApi.mockResolvedValue([]);
  await run(fetchPageInitData('7,8,9'));
  expect(state.taskDetails).toEqual([]);
  expect(fetchWebTaskDetailsByDetailIdsApi).not.toHaveBeenCalled();
});

it('replaces the task scope after navigating to another task', async () => {
  await run(fetchPageInitData('7,8,9'));
  run(initState());
  await run(fetchPageInitData('10'));
  await run(updateFilterConditions(filters));
  expect(fetchWebBriefsApi).toHaveBeenLastCalledWith('10', '1,2', 'shared');
});
