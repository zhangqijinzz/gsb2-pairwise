import { describe, expect, it } from 'vitest';
import type { AnnotationCase, AnnotationEvaluation, AnnotationRound } from '../../api/annotation';
import { getTableProgress } from './tableProgress';

const evaluation = (overrides: Partial<AnnotationEvaluation> = {}) => ({ status: 'ready', evidenceHash: 'current', scores: [4,4,4,4,4], ...overrides }) as AnnotationEvaluation;
const round = (evaluations: AnnotationEvaluation[] = [], overrides: Partial<AnnotationRound> = {}) => ({ status:'complete', evidenceHash:'current', evaluations, ...overrides }) as AnnotationRound;
const item = (rounds: AnnotationRound[], overrides: Partial<AnnotationCase> = {}) => ({rounds,...overrides}) as AnnotationCase;

describe('persistent preparation progress', () => {
  it('counts low scores as ready but distinguishes missing evidence', () => {
    expect(getTableProgress(item([round([evaluation()]), round([evaluation({status:'needs_evidence'})])]))).toMatchObject({prepared:1,reviewed:2,total:2,missing:1,state:'needs_evidence'});
  });
  it('counts real rounds once, uses the latest assessment, and resets on new rounds', () => {
    expect(getTableProgress(item([round([evaluation(),evaluation({status:'needs_evidence'})]), round(), round([], {status:'excluded'})]))).toMatchObject({prepared:0,reviewed:1,total:2});
    expect(getTableProgress(item([round([evaluation(),evaluation()]),round()]))).toMatchObject({prepared:1,reviewed:1,total:2,state:'partial'});
  });
  it('does not present superseded rules or evidence as ready', () => {
    expect(getTableProgress(item([round([evaluation({current:false})])]))).toMatchObject({prepared:0,stale:1,state:'stale'});
    expect(getTableProgress(item([round([evaluation({evidenceHash:'old'})])]))).toMatchObject({prepared:0,stale:1});
  });
  it('restores running and failed states without hiding saved rounds', () => {
    const base=item([round([evaluation()]),round()],{preparation:{jobId:'j',status:'running',progress:40,message:'第 2 轮：等待响应',error:'',startedAt:100,finishedAt:0,lastActivityAt:110}});
    expect(getTableProgress(base,500)).toMatchObject({state:'running',prepared:1,reviewed:1,total:2,quiet:true});
    expect(getTableProgress({...base,preparation:{...base.preparation!,status:'error',error:'执行超时'}},500)).toMatchObject({state:'error',prepared:1,error:'执行超时'});
  });
});
