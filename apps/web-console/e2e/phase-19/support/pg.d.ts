// The slice of node-postgres these specs use, declared locally.
//
// `pg` is not a dependency of this app and must not become one: it is installed
// by the workflow with `npm install --no-save` for the duration of the run
// (see the phase-19 steps in .github/workflows/ci.yml), because the only thing
// that needs it is the audit-log readback in 04 and 07. Without this
// declaration the dynamic import would have no types at all, and the specs
// would reach for `any` to paper over it.
declare module "pg" {
  export interface QueryResult<Row> {
    rowCount: number | null;
    rows: Row[];
  }

  export class Client {
    constructor(config: { connectionString: string });
    connect(): Promise<void>;
    query<Row>(text: string, values?: Array<string | Date>): Promise<QueryResult<Row>>;
    end(): Promise<void>;
  }
}
