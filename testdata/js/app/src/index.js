import { used } from './used.js';
import { leftover } from './unused-export.js';
import { neverUsed } from './used.js';
import './side.js';

export function live() {
  return used() + 1;
}

function localDead() {
  return 0;
}

live();
