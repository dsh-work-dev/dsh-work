export const name = 'dsh-work-account';
export const inject = ['webServer'];
export function apply(ctx) {
  ctx.on('webserver/index-inject', table => {
    table.push({kind: 'global', name: 'dshDesktop', value: {}});
  });
}
