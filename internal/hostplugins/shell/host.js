export const name = 'dsh-work-shell';
export const inject = ['webServer'];
export function apply(ctx, config = {}) {
  const generation = String(config.generation ?? '');
  if (!generation) throw new Error('shell transport generation is required');
  const query = '?generation=' + encodeURIComponent(generation);
  ctx.on('webserver/index-inject', table => {
    table.push({kind: 'global', name: '__WORK_GENERATION__', value: generation});
    table.push({kind: 'global', name: '__DSH_TRANSPORT__', value: {}});
    table.push({kind: 'script-src', placement: 'head', src: '/__work/boot.js' + query});
    table.push({kind: 'html', placement: 'head', html: '<script type="module" src="/__work/bridge.js' + query + '"></script>'});
  });
}
